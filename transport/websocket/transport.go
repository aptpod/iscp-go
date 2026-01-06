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

	negotiationParams NegotiationParams
	ctx               context.Context
	cancel            context.CancelFunc

	// メトリクス関連（内部ではManagedMetricsProviderを保持）
	managedMetrics metrics.ManagedMetricsProvider
	readTimeout       time.Duration
	writeTimeout      time.Duration
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
func (t *Transport) Read() ([]byte, error) {
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

// Writeは、１メッセージ分のデータを書き込みます。
func (t *Transport) Write(bs []byte) error {
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
