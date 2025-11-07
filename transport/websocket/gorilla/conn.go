package gorilla

import (
	"context"
	"io"
	"time"

	gwebsocket "github.com/gorilla/websocket"

	"github.com/aptpod/iscp-go/transport"
	"github.com/aptpod/iscp-go/transport/websocket"
)

// Connは、 gorilla/websocketのConnのラッパーです。
type Conn struct {
	wsconn *gwebsocket.Conn
}

// Newは、Connを返却します。
func New(wsconn *gwebsocket.Conn) *Conn {
	return &Conn{
		wsconn: wsconn,
	}
}

// Pingは、WebSocketのPingを送信します。
func (c *Conn) Ping(ctx context.Context) error {
	return handlerError(c.wsconn.WriteControl(gwebsocket.PongMessage, []byte{}, time.Now().Add(time.Second)))
}

// Readerは、WebSocketのReaderを取得します。
func (c *Conn) Reader(ctx context.Context) (websocket.MessageType, io.Reader, error) {
	tp, rd, err := c.wsconn.NextReader()
	if err != nil {
		return 0, nil, handlerError(err)
	}
	switch tp {
	case gwebsocket.BinaryMessage:
		return websocket.MessageBinary, rd, nil
	case gwebsocket.TextMessage:
		return websocket.MessageText, rd, nil
	}
	panic("unreachable")
}

// Writerは、WebSocketのWriterを取得します。
func (c *Conn) Writer(ctx context.Context, tp websocket.MessageType) (io.WriteCloser, error) {
	switch tp {
	case websocket.MessageBinary:
		res, err := c.wsconn.NextWriter(gwebsocket.BinaryMessage)
		if err != nil {
			return nil, handlerError(err)
		}
		return res, nil
	case websocket.MessageText:
		res, err := c.wsconn.NextWriter(gwebsocket.TextMessage)
		if err != nil {
			return nil, handlerError(err)
		}
		return res, nil
	}
	panic("unreachable")
}

// Closeは、WebSocketをクローズします。
func (c *Conn) Close() error {
	return c.CloseWithStatus(transport.CloseStatusNormal)
}

// CloseWithStatus(transport.CloseStatus)は、WebSocketを指定したステータスでクローズします。
func (c *Conn) CloseWithStatus(status transport.CloseStatus) error {
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
		return handlerError(err)
	}
	return handlerError(c.wsconn.Close())
}
