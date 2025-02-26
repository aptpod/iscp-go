package websocket

import (
	"bytes"
	"compress/flate"
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/aptpod/iscp-go/internal/xio"
	"github.com/aptpod/iscp-go/transport"
	"github.com/aptpod/iscp-go/transport/compress"
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
}

// Newは、WebSocketトランスポートを返却します。
func New(config Config) *Transport {
	ctx, cancel := context.WithCancel(context.Background())
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

	return &t
}

// Readは、１メッセージ分のデータを読み込みます。
func (t *Transport) Read() ([]byte, error) {
	_, rd, err := t.wsconn.Reader(t.ctx)
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
	wr, err := t.wsconn.Writer(t.ctx, MessageBinary)
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

// Closeはトランスポートを閉じます。
func (t *Transport) close(status transport.CloseStatus) error {
	t.wsconn.CloseWithStatus(status)
	t.cancel()
	return nil
}

func (t *Transport) encodeToWithCompression(wr io.Writer, bs []byte) (int, error) {
	buf := bufferPool.Get().(*bytes.Buffer)
	defer func() {
		buf.Reset()
		bufferPool.Put(buf)
	}()

	fwr, err := flate.NewWriter(buf, t.compressConfig.Level)
	if err != nil {
		return 0, err
	}
	if _, err := fwr.Write(bs); err != nil {
		return 0, err
	}
	if err := fwr.Close(); err != nil {
		return 0, err
	}

	n, err := io.Copy(wr, buf)
	return int(n), err
}

func (t *Transport) encodeToWithContextTakeover(wr io.Writer, bs []byte) (int, error) {
	buf := bufferPool.Get().(*bytes.Buffer)
	defer func() {
		buf.Reset()
		bufferPool.Put(buf)
	}()

	t.writeWindowBufMu.Lock()
	defer t.writeWindowBufMu.Unlock()

	fwr, err := flate.NewWriterDict(buf, t.compressConfig.Level, t.writeWindowBuf.Bytes())
	if err != nil {
		return 0, err
	}
	mwr := io.MultiWriter(fwr, t.writeWindowBuf)
	if _, err := mwr.Write(bs); err != nil {
		return 0, err
	}
	if err := fwr.Close(); err != nil {
		return 0, err
	}
	if t.compressConfig.WindowSize() < t.writeWindowBuf.Len() {
		t.writeWindowBuf.Next(t.writeWindowBuf.Len() - t.compressConfig.WindowSize())
	}

	n, err := io.Copy(wr, buf)
	return int(n), err
}

func (t *Transport) decodeFromWithCompression(rd io.Reader) (int, []byte, error) {
	ird := xio.NewCaptureReader(rd)
	frd := flate.NewReader(ird)
	_, m, err := t.decode(frd)
	if err != nil {
		return 0, nil, err
	}
	if err := frd.Close(); err != nil {
		return 0, nil, err
	}
	return ird.ReadBytes, m, nil
}

func (t *Transport) decodeFromWithContextTakeover(rd io.Reader) (int, []byte, error) {
	t.readWindowBufMu.Lock()
	defer t.readWindowBufMu.Unlock()

	ird := xio.NewCaptureReader(rd)
	frd := flate.NewReaderDict(ird, t.readWindowBuf.Bytes())
	trd := io.TeeReader(frd, t.readWindowBuf)
	_, m, err := t.decode(trd)
	if err != nil {
		return 0, nil, err
	}
	if t.compressConfig.WindowSize() < t.readWindowBuf.Len() {
		t.readWindowBuf.Next(t.readWindowBuf.Len() - t.compressConfig.WindowSize())
	}
	if err := frd.Close(); err != nil {
		return 0, nil, err
	}
	return ird.ReadBytes, m, nil
}

func (t *Transport) decode(rd io.Reader) (int, []byte, error) {
	var buffer bytes.Buffer
	if _, err := io.Copy(&buffer, rd); err != nil {
		return 0, nil, err
	}

	return buffer.Len(), buffer.Bytes(), nil
}
