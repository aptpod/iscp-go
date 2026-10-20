package websocket

import (
	"context"
	"io"
	"net"

	cwebsocket "github.com/coder/websocket"

	"github.com/aptpod/iscp-go/v2/transport"
)

// coderConn is a wrapper around the Conn of coder/websocket.
type coderConn struct {
	wsconn         *cwebsocket.Conn
	underlyingConn net.Conn
}

// newCoderConn returns a coderConn.
func newCoderConn(wsconn *cwebsocket.Conn, conn net.Conn) *coderConn {
	return &coderConn{
		wsconn:         wsconn,
		underlyingConn: conn,
	}
}

// Ping sends a WebSocket Ping.
func (c *coderConn) Ping(ctx context.Context) error {
	return c.wsconn.Ping(ctx)
}

// Reader obtains the Reader of the WebSocket.
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

// Writer obtains the Writer of the WebSocket.
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

// Close closes the WebSocket.
func (c *coderConn) Close() error {
	return c.CloseWithStatus(transport.CloseStatusNormal)
}

// CloseWithStatus closes the WebSocket with the specified status.
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

// UnderlyingConn returns the net.Conn underlying the WebSocket.
func (c *coderConn) UnderlyingConn() net.Conn {
	return c.underlyingConn
}

// SetUnderlyingConn sets the net.Conn underlying the WebSocket.
func (c *coderConn) SetUnderlyingConn(conn net.Conn) {
	if conn != nil {
		c.underlyingConn = conn
	}
}
