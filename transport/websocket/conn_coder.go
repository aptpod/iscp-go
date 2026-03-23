package websocket

import (
	"context"
	"io"
	"net"

	cwebsocket "github.com/coder/websocket"

	"github.com/aptpod/iscp-go/transport"
)

// coderConnは、 coder/websocketのConnのラッパーです。
type coderConn struct {
	wsconn         *cwebsocket.Conn
	underlyingConn net.Conn
}

// newCoderConnは、coderConnを返却します。
func newCoderConn(wsconn *cwebsocket.Conn, conn net.Conn) *coderConn {
	return &coderConn{
		wsconn:         wsconn,
		underlyingConn: conn,
	}
}

// Pingは、WebSocketのPingを送信します。
func (c *coderConn) Ping(ctx context.Context) error {
	return c.wsconn.Ping(ctx)
}

// Readerは、WebSocketのReaderを取得します。
func (c *coderConn) Reader(ctx context.Context) (MessageType, io.Reader, error) {
	tp, rd, err := c.wsconn.Reader(ctx)
	if err != nil {
		return 0, nil, coderHandleError(err)
	}
	switch tp {
	case cwebsocket.MessageBinary:
		return MessageBinary, rd, nil
	case cwebsocket.MessageText:
		return MessageText, rd, nil
	}
	panic("unreachable")
}

// Writerは、WebSocketのWriterを取得します。
func (c *coderConn) Writer(ctx context.Context, tp MessageType) (io.WriteCloser, error) {
	switch tp {
	case MessageBinary:
		wr, err := c.wsconn.Writer(ctx, cwebsocket.MessageBinary)
		if err != nil {
			return nil, coderHandleError(err)
		}
		return wr, nil
	case MessageText:
		wr, err := c.wsconn.Writer(ctx, cwebsocket.MessageText)
		if err != nil {
			return nil, coderHandleError(err)
		}
		return wr, nil
	}
	panic("unreachable")
}

// Closeは、WebSocketをクローズします。
func (c *coderConn) Close() error {
	return c.CloseWithStatus(transport.CloseStatusNormal)
}

// CloseWithStatusは、WebSocketを指定したステータスでクローズします。
func (c *coderConn) CloseWithStatus(status transport.CloseStatus) error {
	var code cwebsocket.StatusCode
	switch status {
	case transport.CloseStatusNormal:
		code = cwebsocket.StatusNormalClosure
	case transport.CloseStatusAbnormal:
		code = cwebsocket.StatusAbnormalClosure
	case transport.CloseStatusGoingAway:
		code = cwebsocket.StatusGoingAway
	case transport.CloseStatusInternalError:
		code = cwebsocket.StatusInternalError
	default:
		code = cwebsocket.StatusInternalError
	}
	return c.wsconn.Close(code, "")
}

// UnderlyingConnは、WebSocketの基盤となるnet.Connを返します。
func (c *coderConn) UnderlyingConn() net.Conn {
	return c.underlyingConn
}

// SetUnderlyingConnは、WebSocketの基盤となるnet.Connを設定します。
func (c *coderConn) SetUnderlyingConn(conn net.Conn) {
	if conn != nil {
		c.underlyingConn = conn
	}
}
