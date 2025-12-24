package coder

import (
	"context"
	"io"
	"net"

	cwebsocket "github.com/coder/websocket"

	"github.com/aptpod/iscp-go/transport"
	"github.com/aptpod/iscp-go/transport/websocket"
)

// Connは、 coder/websocketのConnのラッパーです。
type Conn struct {
	wsconn         *cwebsocket.Conn
	underlyingConn net.Conn
}

// Newは、Connを返却します。
func New(wsconn *cwebsocket.Conn) *Conn {
	return &Conn{
		wsconn:         wsconn,
		underlyingConn: nil,
	}
}

// NewWithUnderlyingConnは、underlying connを指定してConnを返却します。
func NewWithUnderlyingConn(wsconn *cwebsocket.Conn, conn net.Conn) *Conn {
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
		return 0, nil, handleError(err)
	}
	switch tp {
	case cwebsocket.MessageBinary:
		return websocket.MessageBinary, rd, nil
	case cwebsocket.MessageText:
		return websocket.MessageText, rd, nil
	}
	panic("unreachable")
}

// Writerは、WebSocketのWriterを取得します。
func (c *Conn) Writer(ctx context.Context, tp websocket.MessageType) (io.WriteCloser, error) {
	switch tp {
	case websocket.MessageBinary:
		wr, err := c.wsconn.Writer(ctx, cwebsocket.MessageBinary)
		if err != nil {
			return nil, handleError(err)
		}
		return wr, nil
	case websocket.MessageText:
		wr, err := c.wsconn.Writer(ctx, cwebsocket.MessageText)
		if err != nil {
			return nil, handleError(err)
		}
		return wr, nil
	}
	panic("unreachable")
}

// Closeは、WebSocketをクローズします。
func (c *Conn) Close() error {
	return c.CloseWithStatus(transport.CloseStatusNormal)
}

// CloseWithStatus implements websocket.Conn.
func (c *Conn) CloseWithStatus(status transport.CloseStatus) error {
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
// DialWithTCPCaptureで取得したnet.Connがある場合はそれを返し、ない場合はnilを返します。
func (c *Conn) UnderlyingConn() net.Conn {
	return c.underlyingConn
}

// SetUnderlyingConnは、WebSocketの基盤となるnet.Connを設定します。
// connがnilの場合、既存の設定は変更されません。
func (c *Conn) SetUnderlyingConn(conn net.Conn) {
	if conn != nil {
		c.underlyingConn = conn
	}
}
