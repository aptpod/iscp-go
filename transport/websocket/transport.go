package websocket

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/klauspost/compress/flate"

	"github.com/aptpod/iscp-go/transport"
	"github.com/aptpod/iscp-go/transport/compress"
	"github.com/aptpod/iscp-go/transport/metrics"
	"github.com/aptpod/iscp-go/transport/protocol"
)

const (
	// maxWebSocketChunkSize は、1つのWebSocketメッセージの最大ペイロードサイズです。
	maxWebSocketChunkSize = protocol.DefaultMaxChunkSize
)

var (
	_ transport.Transport = (*Transport)(nil)
	_ transport.Closer    = (*Transport)(nil)
)

// Transportは、WebSocketトランスポートです。
type Transport struct {
	wsconn      Conn
	messageType MessageType

	compressConfig   compress.Config
	writeWindowBuf   *bytes.Buffer
	writeWindowBufMu sync.Mutex
	readWindowBuf    *bytes.Buffer
	readWindowBufMu  sync.Mutex

	encodeTo   func(io.Writer, []byte) (int, error)
	decodeFrom func(rd io.Reader) (int, []byte, error)

	rxBytesCounter *uint64
	txBytesCounter *uint64

	// useMessageFraming は、メッセージフレーミング（4バイト長プレフィクス + チャンク分割）を有効にするかを示します。
	// true: v2 (trans=ws2) — 複数のWebSocketメッセージにまたがるiSCPメッセージを再構築
	// false: v1 — 1 WebSocketメッセージ = 1 iSCPメッセージ
	useMessageFraming bool

	// readBuf は、WebSocketメッセージの受信バッファです。
	// 複数のWebSocketメッセージにまたがるiSCPメッセージを再構築するために使用します。
	// useMessageFraming=true の場合のみ使用されます。
	readBuf   bytes.Buffer
	readBufMu sync.Mutex

	negotiationParams NegotiationParams
	ctx               context.Context
	cancel            context.CancelFunc

	// メトリクス関連（内部ではManagedMetricsProviderを保持）
	managedMetrics metrics.ManagedMetricsProvider
	readTimeout    time.Duration
	writeTimeout   time.Duration
}

// Newは、WebSocketトランスポートを返却します。
func New(config Config) *Transport {
	ctx, cancel := context.WithCancel(context.Background())
	readTimeout := config.ReadTimeout
	if readTimeout == 0 {
		readTimeout = DefaultReadTimeout
	}
	writeTimeout := config.WriteTimeout
	if writeTimeout == 0 {
		writeTimeout = DefaultWriteTimeout
	}
	t := Transport{
		wsconn:            config.webSocketConnOrPanic(),
		messageType:       MessageBinary,
		compressConfig:    config.NegotiationParams.CompressConfig(config.CompressConfig),
		writeWindowBuf:    &bytes.Buffer{},
		writeWindowBufMu:  sync.Mutex{},
		readWindowBuf:     &bytes.Buffer{},
		readWindowBufMu:   sync.Mutex{},
		rxBytesCounter:    func(u uint64) *uint64 { return &u }(0),
		txBytesCounter:    func(u uint64) *uint64 { return &u }(0),
		useMessageFraming: config.UseMessageFraming,
		negotiationParams: config.NegotiationParams,
		ctx:               ctx,
		cancel:            cancel,
		readTimeout:       readTimeout,
		writeTimeout:      writeTimeout,
	}

	switch {
	case !t.compressConfig.Enable:
		t.encodeTo = func(w io.Writer, b []byte) (int, error) { return w.Write(b) }
		t.decodeFrom = t.decode
	case t.compressConfig.DisableContextTakeover:
		t.encodeTo = t.encodeToWithCompression
		t.decodeFrom = t.decodeFromWithCompression
	default:
		t.writeWindowBuf = bytes.NewBuffer(nil)
		t.readWindowBuf = bytes.NewBuffer(nil)
		t.encodeTo = t.encodeToWithContextTakeover
		t.decodeFrom = t.decodeFromWithContextTakeover
	}

	// ManagedMetricsProviderの初期化
	if conn := t.wsconn.UnderlyingConn(); conn != nil {
		if tcpConn, ok := conn.(*net.TCPConn); ok {
			t.managedMetrics = metrics.NewTCPInfoProvider(tcpConn, 100*time.Millisecond)
		}
	}
	// TCP接続が取得できない場合はnoopを使用
	if t.managedMetrics == nil {
		t.managedMetrics = metrics.NewNopMetricsProvider()
	}

	// ManagedMetricsProviderを開始
	_ = t.managedMetrics.Start()

	return &t
}

// Readは、１メッセージ分のデータを読み込みます。
//
// UseMessageFraming=true (v2, trans=ws2) の場合:
// WebSocketメッセージ境界仕様に従い、4バイトBE長プレフィクスを使用して
// メッセージを再構築します。複数のWebSocketメッセージにまたがる場合は
// バッファリングして完全なメッセージを返却します。
//
// UseMessageFraming=false (v1) の場合:
// 1 WebSocketメッセージ = 1 iSCPメッセージとして読み込みます。
func (t *Transport) Read() ([]byte, error) {
	if !t.useMessageFraming {
		return t.readSimple()
	}
	return t.readFramed()
}

// readSimple は、v1 モードの Read です。
// 1 WebSocketメッセージ = 1 iSCPメッセージとして読み込みます。
func (t *Transport) readSimple() ([]byte, error) {
	ctx, cancel := context.WithTimeout(t.ctx, t.readTimeout)
	defer cancel()

	_, rd, err := t.wsconn.Reader(ctx)
	if err != nil {
		return nil, fmt.Errorf("get reader: %w", err)
	}
	n, m, err := t.decodeFrom(rd)
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	atomic.AddUint64(t.rxBytesCounter, uint64(n))
	return m, nil
}

// readFramed は、v2 モードの Read です。
// 4バイトBE長プレフィクスを使用してメッセージを再構築します。
func (t *Transport) readFramed() ([]byte, error) {
	t.readBufMu.Lock()
	defer t.readBufMu.Unlock()

	// バッファにメッセージ長プレフィクスが揃うまでWebSocketメッセージを読み込む
	for t.readBuf.Len() < protocol.LengthPrefixSize {
		if err := t.readWebSocketMessage(); err != nil {
			return nil, err
		}
	}

	// メッセージ長を読み取る
	msgLen, _ := protocol.DecodeLengthPrefix(t.readBuf.Bytes()[:protocol.LengthPrefixSize])

	// バッファに完全なメッセージが揃うまで読み込む
	totalNeeded := protocol.LengthPrefixSize + int(msgLen)
	for t.readBuf.Len() < totalNeeded {
		if err := t.readWebSocketMessage(); err != nil {
			return nil, err
		}
	}

	// 長さプレフィクスを消費
	t.readBuf.Next(protocol.LengthPrefixSize)

	// メッセージ本体を読み取る
	msgBytes := make([]byte, msgLen)
	if _, err := io.ReadFull(&t.readBuf, msgBytes); err != nil {
		return nil, fmt.Errorf("read message body: %w", err)
	}

	// デコード（圧縮解除含む）
	n, m, err := t.decodeFrom(bytes.NewReader(msgBytes))
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	atomic.AddUint64(t.rxBytesCounter, uint64(n))
	return m, nil
}

// readWebSocketMessage は、1つのWebSocketメッセージを読み込んでreadBufに追加します。
func (t *Transport) readWebSocketMessage() error {
	ctx, cancel := context.WithTimeout(t.ctx, t.readTimeout)
	defer cancel()

	_, rd, err := t.wsconn.Reader(ctx)
	if err != nil {
		return fmt.Errorf("get reader: %w", err)
	}
	if _, err := io.Copy(&t.readBuf, rd); err != nil {
		return fmt.Errorf("read websocket message: %w", err)
	}
	return nil
}

// Writeは、１メッセージ分のデータを書き込みます。
//
// UseMessageFraming=true (v2, trans=ws2) の場合:
// 4バイトBE長プレフィクスを付与して最大8KBのWebSocketメッセージに分割して送信します。
//
// UseMessageFraming=false (v1) の場合:
// 1 iSCPメッセージ = 1 WebSocketメッセージとして書き込みます。
func (t *Transport) Write(bs []byte) error {
	if !t.useMessageFraming {
		return t.writeSimple(bs)
	}
	return t.writeFramed(bs)
}

// writeSimple は、v1 モードの Write です。
// 1 iSCPメッセージ = 1 WebSocketメッセージとして書き込みます。
func (t *Transport) writeSimple(bs []byte) error {
	ctx, cancel := context.WithTimeout(t.ctx, t.writeTimeout)
	defer cancel()

	wr, err := t.wsconn.Writer(ctx, MessageBinary)
	if err != nil {
		return fmt.Errorf("get writer: %w", err)
	}
	defer wr.Close()

	n, err := t.encodeTo(wr, bs)
	if err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	atomic.AddUint64(t.txBytesCounter, uint64(n))

	return nil
}

// writeFramed は、v2 モードの Write です。
// 4バイトBE長プレフィクスを付与して最大8KBのWebSocketメッセージに分割して送信します。
func (t *Transport) writeFramed(bs []byte) error {
	// エンコード（圧縮含む）
	var encodedBuf bytes.Buffer
	n, err := t.encodeTo(&encodedBuf, bs)
	if err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	atomic.AddUint64(t.txBytesCounter, uint64(n))

	// 長さプレフィクスを付与してフレーム化
	framedBuf := protocol.FrameMessage(encodedBuf.Bytes())

	// 最大8KBのWebSocketメッセージに分割して送信
	chunks := protocol.SplitIntoChunks(framedBuf, maxWebSocketChunkSize)
	for _, chunk := range chunks {

		ctx, cancel := context.WithTimeout(t.ctx, t.writeTimeout)
		wr, err := t.wsconn.Writer(ctx, MessageBinary)
		if err != nil {
			cancel()
			return fmt.Errorf("get writer: %w", err)
		}
		if _, err := wr.Write(chunk); err != nil {
			wr.Close()
			cancel()
			return fmt.Errorf("write chunk: %w", err)
		}
		if err := wr.Close(); err != nil {
			cancel()
			return fmt.Errorf("close writer: %w", err)
		}
		cancel()
	}

	return nil
}

// TxBytesCounterValueは、書き込んだ総バイト数を返却します。
func (t *Transport) TxBytesCounterValue() uint64 {
	return atomic.LoadUint64(t.txBytesCounter)
}

// RxBytesCounterValueは、読み込んだ総バイト数を返却します。
func (t *Transport) RxBytesCounterValue() uint64 {
	return atomic.LoadUint64(t.rxBytesCounter)
}

// Closeはトランスポートを閉じます。
func (t *Transport) Close() error {
	return t.CloseWithStatus(transport.CloseStatusNormal)
}

// CloseWithStatusは、指定したステータスでトランスポートを閉じます。
func (t *Transport) CloseWithStatus(status transport.CloseStatus) error {
	if err := t.close(status); err != nil {
		return fmt.Errorf("close transport: %w", err)
	}
	return nil
}

// NegotiationParamsは、ネゴシエーションパラメータを返却します。
func (t *Transport) NegotiationParams() transport.NegotiationParams {
	return t.negotiationParams.NegotiationParams
}

// AsUnreliableは、トランスポートをUnreliableとして返却します。
//
// WebSocketの場合は必ず `nil, false` を返却します。
func (t *Transport) AsUnreliable() (transport.UnreliableTransport, bool) {
	return nil, false
}

// Nameはトランスポート名を返却します。
func (t *Transport) Name() transport.Name {
	return transport.NameWebSocket
}

// MetricsProviderは、読み取り専用のMetricsProviderを返します。
// 返されたMetricsProviderのライフサイクルはTransportが管理します。
func (t *Transport) MetricsProvider() metrics.MetricsProvider {
	return t.managedMetrics
}

// Closeはトランスポートを閉じます。
func (t *Transport) close(status transport.CloseStatus) error {
	// ManagedMetricsProviderのStop
	t.managedMetrics.Stop()

	t.wsconn.CloseWithStatus(status)
	t.cancel()
	return nil
}

func (t *Transport) encodeToWithCompression(wr io.Writer, bs []byte) (int, error) {
	buf := encodeBufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer encodeBufferPool.Put(buf)

	fwr, err := flate.NewWriter(buf, t.compressConfig.Level)
	if err != nil {
		return 0, fmt.Errorf("new flate writer: %w", err)
	}

	if _, err := fwr.Write(bs); err != nil {
		return 0, fmt.Errorf("write: %w", err)
	}

	if err := fwr.Flush(); err != nil {
		return 0, fmt.Errorf("flush: %w", err)
	}

	if err := fwr.Close(); err != nil {
		return 0, fmt.Errorf("close: %w", err)
	}

	written, err := io.Copy(wr, buf)
	if err != nil {
		return 0, fmt.Errorf("write compressed data: %w", err)
	}

	return int(written), nil
}

func (t *Transport) encodeToWithContextTakeover(wr io.Writer, bs []byte) (int, error) {
	buf := encodeBufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer encodeBufferPool.Put(buf)

	t.writeWindowBufMu.Lock()
	defer t.writeWindowBufMu.Unlock()

	fwr, err := flate.NewWriterDict(buf, t.compressConfig.Level, t.writeWindowBuf.Bytes())
	if err != nil {
		return 0, fmt.Errorf("new flate writer dict: %w", err)
	}
	mwr := io.MultiWriter(fwr, t.writeWindowBuf)
	if _, err := mwr.Write(bs); err != nil {
		return 0, fmt.Errorf("write data: %w", err)
	}
	if err := fwr.Flush(); err != nil {
		return 0, fmt.Errorf("flush: %w", err)
	}

	if err := fwr.Close(); err != nil {
		return 0, fmt.Errorf("close: %w", err)
	}

	if t.compressConfig.WindowSize() < t.writeWindowBuf.Len() {
		t.writeWindowBuf.Next(t.writeWindowBuf.Len() - t.compressConfig.WindowSize())
	}

	written, err := io.Copy(wr, buf)
	if err != nil {
		return 0, fmt.Errorf("copy compressed data: %w", err)
	}

	return int(written), nil
}

func (t *Transport) decodeFromWithCompression(rd io.Reader) (int, []byte, error) {
	// NOTE: flate.NewReaderにrdを設定し、io.Copyすると。読み込みが途中で切れてしまうエラーが発生する場合がある。
	// よってrawBufferに一度すべて読み込ませる必要がある。
	rawBuffer := decodeBufferPool.Get().(*bytes.Buffer)
	rawBuffer.Reset()
	defer decodeBufferPool.Put(rawBuffer)
	rawBytes, err := io.Copy(rawBuffer, rd)
	if err != nil {
		return 0, nil, fmt.Errorf("read raw data: %w", err)
	}

	frd := flate.NewReader(rawBuffer)
	defer frd.Close()

	var decompressedBuffer bytes.Buffer
	if _, err := io.Copy(&decompressedBuffer, frd); err != nil {
		return 0, nil, fmt.Errorf("decompress data: %w", err)
	}

	if err := frd.Close(); err != nil {
		return 0, nil, fmt.Errorf("close flate reader: %w", err)
	}

	return int(rawBytes), decompressedBuffer.Bytes(), nil
}

func (t *Transport) decodeFromWithContextTakeover(rd io.Reader) (int, []byte, error) {
	t.readWindowBufMu.Lock()
	defer t.readWindowBufMu.Unlock()

	rawBuffer := decodeBufferPool.Get().(*bytes.Buffer)
	rawBuffer.Reset()
	defer decodeBufferPool.Put(rawBuffer)
	rawBytes, err := io.Copy(rawBuffer, rd)
	if err != nil {
		return 0, nil, fmt.Errorf("read raw data: %w", err)
	}

	frd := flate.NewReaderDict(rawBuffer, t.readWindowBuf.Bytes())
	defer frd.Close()

	var decompressedBuffer bytes.Buffer
	trd := io.TeeReader(frd, t.readWindowBuf)
	if _, err := io.Copy(&decompressedBuffer, trd); err != nil {
		return 0, nil, fmt.Errorf("decompress data: %w", err)
	}

	if t.compressConfig.WindowSize() < t.readWindowBuf.Len() {
		t.readWindowBuf.Next(t.readWindowBuf.Len() - t.compressConfig.WindowSize())
	}

	if err := frd.Close(); err != nil {
		return 0, nil, fmt.Errorf("close flate reader: %w", err)
	}

	return int(rawBytes), decompressedBuffer.Bytes(), nil
}

func (t *Transport) decode(rd io.Reader) (int, []byte, error) {
	var buffer bytes.Buffer
	if _, err := io.Copy(&buffer, rd); err != nil {
		return 0, nil, err
	}

	return buffer.Len(), buffer.Bytes(), nil
}
