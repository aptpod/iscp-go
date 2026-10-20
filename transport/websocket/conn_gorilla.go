package websocket

import (
	"context"
	"io"
	"net"
	"time"

	gwebsocket "github.com/gorilla/websocket"

	"github.com/aptpod/iscp-go/v2/transport"
)

// gorillaConn is a wrapper around the Conn of gorilla/websocket.
type gorillaConn struct {
	wsconn *gwebsocket.Conn
}

// newGorillaConn returns a gorillaConn.
func newGorillaConn(wsconn *gwebsocket.Conn) *gorillaConn {
	return &gorillaConn{
		wsconn: wsconn,
	}
}

// Ping sends a WebSocket Ping.
func (c *gorillaConn) Ping(ctx context.Context) error {
	return gorillaHandleError(c.wsconn.WriteControl(gwebsocket.PongMessage, []byte{}, time.Now().Add(time.Second)))
}

// Reader obtains the Reader of the WebSocket.
func (c *gorillaConn) Reader(ctx context.Context) (MessageType, io.Reader, error) {
	tp, rd, err := c.wsconn.NextReader()
	if err != nil {
		return 0, nil, gorillaHandleError(err)
	}
	switch tp {
	case gwebsocket.BinaryMessage:
		return MessageBinary, rd, nil
	case gwebsocket.TextMessage:
		return MessageText, rd, nil
	}
	panic("unreachable")
}

// Writer obtains the Writer of the WebSocket.
func (c *gorillaConn) Writer(ctx context.Context, tp MessageType) (io.WriteCloser, error) {
	switch tp {
	case MessageBinary:
		res, err := c.wsconn.NextWriter(gwebsocket.BinaryMessage)
		if err != nil {
			return nil, gorillaHandleError(err)
		}
		return res, nil
	case MessageText:
		res, err := c.wsconn.NextWriter(gwebsocket.TextMessage)
		if err != nil {
			return nil, gorillaHandleError(err)
		}
		return res, nil
	}
	panic("unreachable")
}

// Close closes the WebSocket.
func (c *gorillaConn) Close() error {
	return c.CloseWithStatus(transport.CloseStatusNormal)
}

// CloseWithStatus closes the WebSocket with the specified status.
func (c *gorillaConn) CloseWithStatus(status transport.CloseStatus) error {
	var code int
	switch status {
	case transport.CloseStatusNormal:
		code = gwebsocket.CloseNormalClosure
	case transport.CloseStatusGoingAway:
		code = gwebsocket.CloseGoingAway
	case transport.CloseStatusAbnormal:
		code = gwebsocket.CloseAbnormalClosure
	case transport.CloseStatusInternalError:
		code = gwebsocket.CloseInternalServerErr
	default:
		code = gwebsocket.CloseInternalServerErr
	}

	if err := c.wsconn.CloseHandler()(code, ""); err != nil {
		return gorillaHandleError(err)
	}
	return gorillaHandleError(c.wsconn.Close())
}

// UnderlyingConn returns the net.Conn underlying the WebSocket.
func (c *gorillaConn) UnderlyingConn() net.Conn {
	return c.wsconn.UnderlyingConn()
}

// SetUnderlyingConn sets the net.Conn underlying the WebSocket.
// gorilla/websocket can obtain it directly with wsconn.UnderlyingConn(), so it does not
// need to be set from outside. This method is a no-op implementation to satisfy the interface.
func (c *gorillaConn) SetUnderlyingConn(conn net.Conn) {
	// Do nothing, because gorilla/websocket can obtain the UnderlyingConn directly internally
}
