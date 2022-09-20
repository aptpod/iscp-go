package nhooyr

import (
	"context"
	"io"

	"github.com/aptpod/iscp-go/transport/websocket"
	nwebsocket "nhooyr.io/websocket"
)

// Connは、 nhooyr.io/websocketのConnのラッパーです。
type Conn struct {
	wsconn *nwebsocket.Conn
}

// Newは、Connを返却します。
func New(wsconn *nwebsocket.Conn) *Conn {
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
	return c.wsconn.Close(nwebsocket.StatusNormalClosure, "")
}
