package websocket

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/klauspost/compress/flate"

	"github.com/aptpod/iscp-go/v2/errors"
	"github.com/aptpod/iscp-go/v2/transport"
	"github.com/aptpod/iscp-go/v2/transport/compress"
	"github.com/aptpod/iscp-go/v2/transport/metrics"
	"github.com/aptpod/iscp-go/v2/transport/protocol"
)

const (
	// maxWebSocketChunkSize is the maximum payload size of a single WebSocket message.
	maxWebSocketChunkSize = protocol.DefaultMaxChunkSize
)

var (
	_ transport.Transport = (*Transport)(nil)
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

	// useMessageFraming indicates whether message framing (4-byte length prefix + chunk splitting) is enabled.
	// true: v2 (trans=ws2) - reassembles an iSCP message that spans multiple WebSocket messages
	// false: v1 - 1 WebSocket message = 1 iSCP message
	useMessageFraming bool

	// readBuf is the receive buffer for WebSocket messages.
	// It is used to reassemble an iSCP message that spans multiple WebSocket messages.
	// It is used only when useMessageFraming=true.
	readBuf   bytes.Buffer
	readBufMu sync.Mutex

	negotiationParams transport.NegotiationParams
	ctx               context.Context
	cancel            context.CancelFunc

	// Metrics related (internally holds a ManagedMetricsProvider)
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

	// Initialize the ManagedMetricsProvider
	if conn := t.wsconn.UnderlyingConn(); conn != nil {
		if tcpConn, ok := conn.(*net.TCPConn); ok {
			t.managedMetrics = metrics.NewTCPInfoProvider(tcpConn, 100*time.Millisecond)
		}
	}
	// Use noop if the TCP connection cannot be obtained
	if t.managedMetrics == nil {
		t.managedMetrics = metrics.NewNopMetricsProvider()
	}

	// Start the ManagedMetricsProvider
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

// readSimple is Read for v1 mode.
// It reads 1 WebSocket message = 1 iSCP message.
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

// readFramed is Read for v2 mode.
// It reassembles a message using the 4-byte BE length prefix.
func (t *Transport) readFramed() ([]byte, error) {
	t.readBufMu.Lock()
	defer t.readBufMu.Unlock()

	// Read WebSocket messages until the buffer holds the message length prefix
	for t.readBuf.Len() < protocol.LengthPrefixSize {
		if err := t.readWebSocketMessage(); err != nil {
			return nil, err
		}
	}

	// Read the message length
	msgLen, _ := protocol.DecodeLengthPrefix(t.readBuf.Bytes()[:protocol.LengthPrefixSize])

	// Read until the buffer holds the complete message
	totalNeeded := protocol.LengthPrefixSize + int(msgLen)
	for t.readBuf.Len() < totalNeeded {
		if err := t.readWebSocketMessage(); err != nil {
			return nil, err
		}
	}

	// Consume the length prefix
	t.readBuf.Next(protocol.LengthPrefixSize)

	// Read the message body
	msgBytes := make([]byte, msgLen)
	if _, err := io.ReadFull(&t.readBuf, msgBytes); err != nil {
		return nil, fmt.Errorf("read message body: %w", err)
	}

	// Decode (including decompression)
	n, m, err := t.decodeFrom(bytes.NewReader(msgBytes))
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	atomic.AddUint64(t.rxBytesCounter, uint64(n))
	return m, nil
}

// readWebSocketMessage reads one WebSocket message and appends it to readBuf.
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

// writeSimple is Write for v1 mode.
// It writes 1 iSCP message = 1 WebSocket message.
func (t *Transport) writeSimple(bs []byte) error {
	ctx, cancel := context.WithTimeout(t.ctx, t.writeTimeout)
	defer cancel()

	wr, err := t.wsconn.Writer(ctx, MessageBinary)
	if err != nil {
		return fmt.Errorf("get writer: %w", err)
	}

	n, err := t.encodeTo(wr, bs)
	if err != nil {
		wr.Close()
		if isWriteConnectionClosedError(err) {
			return fmt.Errorf("encode: %+v: %w", err, transport.ErrAlreadyClosed)
		}
		return fmt.Errorf("encode: %w", err)
	}
	atomic.AddUint64(t.txBytesCounter, uint64(n))

	if err := wr.Close(); err != nil {
		if isWriteConnectionClosedError(err) {
			return fmt.Errorf("close writer: %+v: %w", err, transport.ErrAlreadyClosed)
		}
		return fmt.Errorf("close writer: %w", err)
	}
	return nil
}

// writeFramed is Write for v2 mode.
// It prepends the 4-byte BE length prefix, splits the data into WebSocket messages of at most 8KB, and sends them.
func (t *Transport) writeFramed(bs []byte) error {
	// Encode (including compression)
	var encodedBuf bytes.Buffer
	n, err := t.encodeTo(&encodedBuf, bs)
	if err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	atomic.AddUint64(t.txBytesCounter, uint64(n))

	// Add the length prefix to frame the message
	framedBuf := protocol.FrameMessage(encodedBuf.Bytes())

	// Split into WebSocket messages of at most 8KB and send
	chunks := protocol.SplitIntoChunks(framedBuf, maxWebSocketChunkSize)
	for i, chunk := range chunks {

		ctx, cancel := context.WithTimeout(t.ctx, t.writeTimeout)
		wr, err := t.wsconn.Writer(ctx, MessageBinary)
		if err != nil {
			cancel()
			// For the same reason as the i == 0 case (for the second and later chunks, the first
			// one may already have reached the peer, so an unconditional conversion would cause
			// duplicate partial sends), when i > 0, only when the error at the Writer() acquisition
			// stage can be determined as transport.ErrAlreadyClosed, drop just that determination.
			// gorillaHandleError/coderHandleError unconditionally wrap a Writer() acquisition
			// failure in transport.ErrAlreadyClosed, so propagating it as is with %w would make
			// the guard for i == 0 meaningless.
			//
			// However, cutting the whole error chain with %v would also lose classifications that
			// do not need to be stripped. In the two current implementations, neither a normal
			// close (transport.IsNormalClose) nor context.DeadlineExceeded reaches the Writer()
			// acquisition path (coder's mu.lock returns only net.ErrClosed or a ctx error, and
			// gorilla's *CloseError is generated only on the read path), so currently there is no
			// observable difference even if these classifications are kept. Even so, we limit what
			// is stripped to the minimum necessary (only when it is determined as ErrAlreadyClosed),
			// align the criterion with i>0 of wr.Write/wr.Close, and propagate everything else with
			// %w as is. This also guards against a regression in case the wrappers start
			// attaching these classifications in the future.
			if i > 0 && errors.Is(err, transport.ErrAlreadyClosed) {
				return fmt.Errorf("get writer at chunk %d: %+v", i, err)
			}
			return fmt.Errorf("get writer: %w", err)
		}
		if _, err := wr.Write(chunk); err != nil {
			wr.Close()
			cancel()
			// Only when i == 0 (not a single byte has been sent yet), convert a closure-caused
			// error to ErrAlreadyClosed and allow the fallback.
			// If it fails on the second or later chunk, the first one may already have reached
			// the peer, so an unconditional conversion would cause duplicate partial sends (unchanged).
			if i == 0 && isWriteConnectionClosedError(err) {
				return fmt.Errorf("write chunk: %+v: %w", err, transport.ErrAlreadyClosed)
			}
			return fmt.Errorf("write chunk: %w", err)
		}
		if err := wr.Close(); err != nil {
			cancel()
			if i == 0 && isWriteConnectionClosedError(err) {
				return fmt.Errorf("close writer: %+v: %w", err, transport.ErrAlreadyClosed)
			}
			return fmt.Errorf("close writer: %w", err)
		}
		cancel()
	}

	return nil
}

// isWriteConnectionClosedError determines whether the error returned by a Write/Close call
// after obtaining a Writer is caused by the underlying connection being closed.
//
// The io.WriteCloser returned by wsconn.Writer does not go through the error wrapping
// (gorillaHandleError / coderHandleError) applied at the Reader/Writer acquisition stage
// in either the gorilla or coder implementation, so here we do a determination equivalent to
// isErrTransportClosed of QUIC (transport/quic/transport.go).
func isWriteConnectionClosedError(err error) bool {
	if err == nil {
		return false
	}
	// context.Canceled comes from the transport's Close (cancel of t.ctx).
	// context.DeadlineExceeded comes from the expiry of writeTimeout, and coder/websocket's
	// timeoutLoop closes the connection when the write ctx expires, so both mean
	// "the connection can no longer be used" (a gorilla write timeout becomes
	// *net.OpError and is caught by the branch below). Note that it is intentional not to treat
	// DeadlineExceeded as closed at the Writer acquisition stage (coderHandleError):
	// there, the expiry may be from waiting for a lock, and the connection may still be alive.
	if errors.Is(err, net.ErrClosed) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr *net.OpError
	if errors.As(err, &netErr) {
		return true
	}
	var sysCallErr *os.SyscallError
	return errors.As(err, &sysCallErr)
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
	return t.negotiationParams
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

// close closes the transport.
func (t *Transport) close(status transport.CloseStatus) error {
	// Stop the ManagedMetricsProvider
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
	// NOTE: If rd is set to flate.NewReader and io.Copy is used, an error where the read is cut off midway may occur.
	// Therefore, everything needs to be read into rawBuffer once.
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
