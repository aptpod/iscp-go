package nhooyr

import (
	"context"
	"io"
	"net"

	nwebsocket "nhooyr.io/websocket"

	"github.com/aptpod/iscp-go/transport"
	"github.com/aptpod/iscp-go/transport/websocket"
)

// Connは、 nhooyr.io/websocketのConnのラッパーです。
type Conn struct {
	wsconn         *nwebsocket.Conn
	underlyingConn net.Conn
}

// Newは、Connを返却します。
func New(wsconn *nwebsocket.Conn) *Conn {
	return &Conn{
		wsconn:         wsconn,
		underlyingConn: nil,
	}
}

// NewWithUnderlyingConnは、underlying connを指定してConnを返却します。
func NewWithUnderlyingConn(wsconn *nwebsocket.Conn, conn net.Conn) *Conn {
	return &Conn{
		wsconn:         wsconn,
		underlyingConn: conn,
	}
}

// Pingは、WebSocketのPingを送信します。
func (c *Conn) Ping(ctx context.Context) error {
	return c.wsconn.Ping(ctx)
}

// Readerは、WebSocketのReaderを取得します。
func (c *Conn) Reader(ctx context.Context) (websocket.MessageType, io.Reader, error) {
	tp, rd, err := c.wsconn.Reader(ctx)
	if err != nil {
		return 0, nil, err
	}
	switch tp {
	case nwebsocket.MessageBinary:
		return websocket.MessageBinary, rd, nil
	case nwebsocket.MessageText:
		return websocket.MessageBinary, rd, nil
	}
	panic("unreachable")
}

// Writerは、WebSocketのWriterを取得します。
func (c *Conn) Writer(ctx context.Context, tp websocket.MessageType) (io.WriteCloser, error) {
	switch tp {
	case websocket.MessageBinary:
		return c.wsconn.Writer(ctx, nwebsocket.MessageBinary)
	case websocket.MessageText:
		return c.wsconn.Writer(ctx, nwebsocket.MessageText)
	}
	panic("unreachable")
}

// Closeは、WebSocketをクローズします。
func (c *Conn) Close() error {
	return c.CloseWithStatus(transport.CloseStatusNormal)
}

// CloseWithStatus implements websocket.Conn.
func (c *Conn) CloseWithStatus(status transport.CloseStatus) error {
	var code nwebsocket.StatusCode
	switch status {
	case transport.CloseStatusNormal:
		code = nwebsocket.StatusNormalClosure
	case transport.CloseStatusAbnormal:
		code = nwebsocket.StatusAbnormalClosure
	case transport.CloseStatusGoingAway:
		code = nwebsocket.StatusGoingAway
	case transport.CloseStatusInternalError:
		code = nwebsocket.StatusInternalError
	default:
		code = nwebsocket.StatusInternalError
	}
	return c.wsconn.Close(code, "")
}

// UnderlyingConnは、WebSocketの基盤となるnet.Connを返します。
// DialWithTCPCaptureで取得したnet.Connがある場合はそれを返し、ない場合はnilを返します。
func (c *Conn) UnderlyingConn() net.Conn {
	return c.underlyingConn
}
