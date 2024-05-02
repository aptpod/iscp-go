package webtransport

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aptpod/iscp-go/errors"

	"github.com/aptpod/iscp-go/internal/segment"
	"github.com/aptpod/iscp-go/transport"
	"github.com/aptpod/iscp-go/transport/compress"
	quic "github.com/quic-go/quic-go"
	"github.com/quic-go/webtransport-go"
	webtransgo "github.com/quic-go/webtransport-go"
)

// for test
var clearReadBufferInterval = time.Second

// Transportは、WebTransportのトランスポートです。
type Transport struct {
	conn            *webtransgo.Session
	sendMu          sync.Mutex
	sendStream      webtransgo.SendStream
	readC           chan readBinarySet
	decodeFunc      func([]byte) ([]byte, error)
	encodeFunc      func(bs []byte, compressionLevel int) ([]byte, error)
	readUnreliableC chan readBinarySet

	readBufferForUnreliable *segment.ReadBuffers

	negotiationParams NegotiationParams

	rxBytesCounter *uint64
	txBytesCounter *uint64

	compressConfig compress.Config

	sequenceNumber uint32

	cancel context.CancelFunc
}

type readBinarySet struct {
	msg []byte
	err error
}

/*
New は、 WebTransport 向けのトランスポートを生成します。
*/
func New(config Config) (*Transport, error) {
	if config.ReadBufferExpiry == 0 {
		config.ReadBufferExpiry = time.Second * 10
	}
	t := Transport{
		conn:            config.connectionOrPanic(),
		readC:           make(chan readBinarySet, config.queueSizeOrDefault()),
		readUnreliableC: make(chan readBinarySet, config.queueSizeOrDefault()),
		readBufferForUnreliable: &segment.ReadBuffers{
			ReadBuffer:       map[uint32]*segment.ReadBuffer{},
			ReadBufferExpiry: config.ReadBufferExpiry,
		},
		compressConfig:    config.NegotiationParams.CompressConfig(config.CompressConfig),
		negotiationParams: config.NegotiationParams,
		rxBytesCounter:    func(u uint64) *uint64 { return &u }(0),
		txBytesCounter:    func(u uint64) *uint64 { return &u }(0),
		sequenceNumber:    math.MaxUint32,
	}

	switch {
	case !t.compressConfig.Enable:
		t.decodeFunc = func(b []byte) ([]byte, error) { return b, nil }
		t.encodeFunc = func(b []byte, _ int) ([]byte, error) { return b, nil }
	default:
		t.decodeFunc = decodeWithCompression
		t.encodeFunc = encodeWithCompression
	}

	sendStream, err := t.conn.OpenUniStream()
	if err != nil {
		return nil, err
	}
	t.sendStream = sendStream

	// Read Goroutine
	ctx, cancel := context.WithCancel(context.Background())
	t.cancel = cancel
	go func() {
		defer close(t.readC)
		rcvStream, err := t.conn.AcceptUniStream(context.TODO())
		if err != nil {
			if isErrTooManyOpenSteams(err) {
				t.conn.CloseWithError(0, "")
			}
			if isErrTransportClosed(err) {
				return
			}
			t.conn.CloseWithError(0, "")
			return
		}
		for {

			bs, err := t.decodeFrom(rcvStream)
			if err != nil {
				if isErrTooManyOpenSteams(err) {
					t.conn.CloseWithError(0, "")
				}
				if isErrTransportClosed(err) {
					return
				}
				t.conn.CloseWithError(0, "")
				return
			}

			select {
			case t.readC <- readBinarySet{msg: bs}:
			case <-ctx.Done():
				return
			}

		}
	}()

	go func() {
		// clear read buffer loop
		ticker := time.NewTicker(clearReadBufferInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			t.readBufferForUnreliable.RemoveExpired()

		}
	}()

	go func() {
		defer func() {
			if err := recover(); err != nil {
				panic(fmt.Sprintf("Required `EnableDatagrams` Option: %+v", err))
			}
		}()
		defer close(t.readUnreliableC)
		for {
			bs, err := t.conn.ReceiveDatagram(ctx)
			if err != nil {
				if err == io.EOF {
					// want until closed streamCh
					continue
				}
				select {
				case <-ctx.Done():
					return
				case t.readUnreliableC <- readBinarySet{err: err}:
					continue
				}
			}

			atomic.AddUint64(t.rxBytesCounter, uint64(len(bs)))
			m, finished, err := t.receiveMessage(bs)
			if err != nil {
				if err == io.EOF {
					// want until closed streamCh
					continue
				}
				select {
				case <-ctx.Done():
					return
				case t.readUnreliableC <- readBinarySet{err: err}:
					continue
				}
			}
			if !finished {
				continue
			}
			select {
			case t.readUnreliableC <- readBinarySet{msg: m}:
			case <-ctx.Done():
				return
			}

		}
	}()

	return &t, nil
}

func (t *Transport) receiveMessage(bs []byte) ([]byte, bool, error) {
	m, ok, err := t.readBufferForUnreliable.Receive(bs)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, nil
	}
	m, err = t.decodeFunc(m)
	if err != nil {
		return nil, false, err
	}
	return m, true, nil
}

// Readは、１メッセージ分のデータを読み込みます。
func (t *Transport) Read() ([]byte, error) {
	set, ok := <-t.readC
	if !ok {
		return nil, transport.ErrAlreadyClosed
	}
	if err := set.err; err != nil {
		if isErrTransportClosed(err) {
			return nil, transport.ErrAlreadyClosed
		}
		return nil, err
	}
	return set.msg, nil
}

// Writeは、１メッセージ分のデータを書き込みます。
func (t *Transport) Write(m []byte) error {
	t.sendMu.Lock()
	defer t.sendMu.Unlock()
	bs, err := t.encodeFunc(m, t.compressConfig.Level)
	if err != nil {
		return err
	}

	n, err := writeTo(t.sendStream, bs)
	if err != nil {
		return err
	}

	atomic.AddUint64(t.txBytesCounter, uint64(n))
	return nil
}

func writeTo(wr io.Writer, payload []byte) (int, error) {
	bytesMsgLength := make([]byte, 4)
	binary.BigEndian.PutUint32(bytesMsgLength, uint32(len(payload)))
	if _, err := wr.Write(bytesMsgLength); err != nil {
		return 0, err
	}

	if _, err := wr.Write(payload); err != nil {
		return 0, err
	}

	return 4 + len(payload), nil
}

// ReadUnreliableは、信頼性のないトランスポートから１メッセージ分のデータを読み込みます。
func (t *Transport) ReadUnreliable() ([]byte, error) {
	set, ok := <-t.readUnreliableC
	if !ok {
		return nil, transport.ErrAlreadyClosed
	}
	if err := set.err; err != nil {
		if isErrTransportClosed(err) {
			return nil, transport.ErrAlreadyClosed
		}
		return nil, err
	}
	return set.msg, nil
}

// WriteUnreliableは、信頼性のないトランスポートへ１メッセージ分のデータを書き込みます。
func (t *Transport) WriteUnreliable(m []byte) error {
	// TODO segmentation
	m, err := t.encodeFunc(m, t.compressConfig.Level)
	if err != nil {
		if isErrTransportClosed(err) {
			return transport.ErrAlreadyClosed
		}
		return err
	}
	n, err := segment.SendTo(t.conn, atomic.AddUint32(&t.sequenceNumber, 1), m)
	if err != nil {
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
	defer t.cancel()
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
// トランスポートにWebTransportを選択した場合、信頼性のないトランスポートとしてはQUICDatagramが使用されます。
func (t *Transport) AsUnreliable() (transport.UnreliableTransport, bool) {
	return &datagram{t: t}, true
}

func (t *Transport) close() error {
	t.cancel()
	return t.conn.CloseWithError(0, "")
}

func (t *Transport) decodeFrom(rd io.Reader) ([]byte, error) {
	// TODO: optimization
	bytesMsgLength := make([]byte, 4)
	if _, err := io.ReadFull(rd, bytesMsgLength); err != nil {
		return nil, err
	}
	msgLength := binary.BigEndian.Uint32(bytesMsgLength)

	bs := make([]byte, msgLength)
	if _, err := io.ReadFull(rd, bs); err != nil {
		return nil, err
	}

	atomic.AddUint64(t.rxBytesCounter, uint64(4+msgLength))
	return t.decodeFunc(bs)
}

// Nameはトランスポート名を返却します。
func (t *Transport) Name() transport.Name {
	return transport.NameWebTransport
}

func isErrTransportClosed(err error) bool {
	if err == context.Canceled {
		return true
	}

	var seserr *webtransport.SessionError
	if errors.As(err, &seserr) {
		if seserr.ErrorCode == 0 {
			return true
		}
	}

	var streamerr *webtransport.SessionError
	if errors.As(err, &streamerr) {
		if streamerr.ErrorCode == 0 {
			return true
		}
	}

	var aerr *quic.ApplicationError
	if errors.As(err, &aerr) {
		if aerr.ErrorCode == 0 {
			return true
		}
	}

	var qerr *quic.TransportError
	if errors.As(err, &qerr) {
		if qerr.ErrorCode == quic.ApplicationErrorErrorCode {
			return true
		}
	}

	return false
}

func isErrTooManyOpenSteams(err error) bool {
	return err.Error() == "too many open streams"
}

func encodeWithCompression(bs []byte, level int) ([]byte, error) {
	// TODO: optimization
	var buf bytes.Buffer
	zwr, err := zlib.NewWriterLevel(&buf, level)
	if err != nil {
		return nil, err
	}
	if _, err := zwr.Write(bs); err != nil {
		return nil, err
	}
	if err := zwr.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), err
}

func decodeWithCompression(bs []byte) ([]byte, error) {
	// TODO: optimization
	buf := bytes.NewBuffer(bs)

	zrd, err := zlib.NewReader(buf)
	if err != nil {
		return nil, err
	}

	m, err := io.ReadAll(zrd)
	if err != nil {
		return nil, err
	}
	if err := zrd.Close(); err != nil {
		return nil, err
	}

	return m, nil
}
