package websocket

import (
	"bytes"
	"compress/flate"
	"context"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aptpod/iscp-go/errors"
	"github.com/aptpod/iscp-go/internal/xio"
	"github.com/aptpod/iscp-go/transport"
	"github.com/aptpod/iscp-go/transport/compress"
)

// Transportは、WebSocketトランスポートです。
type Transport struct {
	wrmu        sync.Mutex
	wsconn      Conn
	readC       chan readBinarySet
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

	ctx    context.Context
	cancel context.CancelFunc
}

// Newは、WebSocketトランスポートを返却します。
func New(config Config) *Transport {
	ctx, cancel := context.WithCancel(context.Background())
	t := Transport{
		wsconn:            config.webSocketConnOrPanic(),
		readC:             make(chan readBinarySet, config.queueSizeOrDefault()),
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

	go t.readLoop()
	go t.keepAliveLoop()
	return &t
}

func (t *Transport) keepAliveLoop() {
	keepAliveTicker := time.NewTicker(10 * time.Second)
	defer keepAliveTicker.Stop()
	for {
		select {
		case <-keepAliveTicker.C:
		case <-t.ctx.Done():
			return
		}
		if err := t.wsconn.Ping(t.ctx); err != nil {
			if isErrTransportClosed(err) {
				return
			}
		}
	}
}

func (t *Transport) readLoop() {
	defer close(t.readC)
	for {
		_, rd, err := t.wsconn.Reader(t.ctx)
		if err != nil {
			if isErrTransportClosed(err) {
				return
			}
			select {
			case <-t.ctx.Done():
				return
			case t.readC <- readBinarySet{err: err}:
			}
			return
		}
		n, m, err := t.decodeFrom(rd)
		if err != nil {
			select {
			case <-t.ctx.Done():
				return
			case t.readC <- readBinarySet{err: err}:
				continue
			}
		}

		atomic.AddUint64(t.rxBytesCounter, uint64(n))

		select {
		case <-t.ctx.Done():
			return
		case t.readC <- readBinarySet{msg: m}:
			continue
		}
	}
}

type readBinarySet struct {
	msg []byte
	err error
}

// Readは、１メッセージ分のデータを読み込みます。
func (t *Transport) Read() ([]byte, error) {
	select {
	case msg, ok := <-t.readC:
		if !ok {
			return nil, transport.ErrAlreadyClosed
		}
		if msg.err != nil {
			return nil, msg.err
		}
		return msg.msg, nil
	case <-t.ctx.Done():
		return nil, transport.ErrAlreadyClosed
	}
}

// Writeは、１メッセージ分のデータを書き込みます。
func (t *Transport) Write(bs []byte) error {
	t.wrmu.Lock()
	defer t.wrmu.Unlock()
	wr, err := t.wsconn.Writer(t.ctx, MessageBinary)
	if err != nil {
		if isErrTransportClosed(err) {
			return errors.Errorf("%+v: %w", err, transport.ErrAlreadyClosed)
		}
		select {
		case <-t.ctx.Done():
			return errors.Errorf("%+v: %w", err, transport.ErrAlreadyClosed)
		default:
		}
		return err
	}
	defer wr.Close()

	n, err := t.encodeTo(wr, bs)
	if err != nil {
		select {
		case <-t.ctx.Done():
			return transport.ErrAlreadyClosed
		default:
		}
		if isErrTransportClosed(err) {
			return errors.Errorf("%+v: %w", err, transport.ErrAlreadyClosed)
		}
		return err
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
	if err := t.close(); err != nil {
		if isErrTransportClosed(err) {
			return transport.ErrAlreadyClosed
		}
		return err
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
func (t *Transport) close() error {
	t.cancel()
	t.wsconn.Close()
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
