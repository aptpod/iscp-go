package coder

import (
	"context"
	"io"

	"github.com/aptpod/iscp-go/transport"
	"github.com/aptpod/iscp-go/transport/websocket"
	cwebsocket "github.com/coder/websocket"
)

// Connは、 gorilla/websocketのConnのラッパーです。
type Conn struct {
	wsconn *cwebsocket.Conn
}

// Newは、Connを返却します。
func New(wsconn *cwebsocket.Conn) *Conn {
	return &Conn{
		wsconn: wsconn,
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
