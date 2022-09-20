package gorilla

import (
	"context"
	"io"
	"time"

	"github.com/aptpod/iscp-go/transport/websocket"
	gwebsocket "github.com/gorilla/websocket"
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
	return c.wsconn.WriteControl(gwebsocket.PongMessage, []byte{}, time.Now().Add(time.Second))
}

// Readerは、WebSocketのReaderを取得します。
func (c *Conn) Reader(ctx context.Context) (websocket.MessageType, io.Reader, error) {
	tp, rd, err := c.wsconn.NextReader()
	if err != nil {
		return 0, nil, err
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
		return c.wsconn.NextWriter(gwebsocket.BinaryMessage)
	case websocket.MessageText:
		return c.wsconn.NextWriter(gwebsocket.TextMessage)
	}
	panic("unreachable")
}

// Closeは、WebSocketをクローズします。
func (c *Conn) Close() error {
	if err := c.wsconn.CloseHandler()(gwebsocket.CloseNormalClosure, ""); err != nil {
		return err
	}
	return c.wsconn.Close()
}
