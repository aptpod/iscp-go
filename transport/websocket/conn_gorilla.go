package websocket

import (
	"context"
	"io"
	"net"
	"time"

	gwebsocket "github.com/gorilla/websocket"

	"github.com/aptpod/iscp-go/transport"
)

// gorillaConnは、 gorilla/websocketのConnのラッパーです。
type gorillaConn struct {
	wsconn *gwebsocket.Conn
}

// newGorillaConnは、gorillaConnを返却します。
func newGorillaConn(wsconn *gwebsocket.Conn) *gorillaConn {
	return &gorillaConn{
		wsconn: wsconn,
	}
}

// Pingは、WebSocketのPingを送信します。
func (c *gorillaConn) Ping(ctx context.Context) error {
	return gorillaHandleError(c.wsconn.WriteControl(gwebsocket.PongMessage, []byte{}, time.Now().Add(time.Second)))
}

// Readerは、WebSocketのReaderを取得します。
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

// Writerは、WebSocketのWriterを取得します。
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

// Closeは、WebSocketをクローズします。
func (c *gorillaConn) Close() error {
	return c.CloseWithStatus(transport.CloseStatusNormal)
}

// CloseWithStatusは、WebSocketを指定したステータスでクローズします。
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

// UnderlyingConnは、WebSocketの基盤となるnet.Connを返します。
func (c *gorillaConn) UnderlyingConn() net.Conn {
	return c.wsconn.UnderlyingConn()
}

// SetUnderlyingConnは、WebSocketの基盤となるnet.Connを設定します。
// gorilla/websocketは wsconn.UnderlyingConn() で直接取得可能なため、
// 外部から設定する必要はありません。このメソッドはインターフェース準拠のためのno-op実装です。
func (c *gorillaConn) SetUnderlyingConn(conn net.Conn) {
	// gorilla/websocketは内部でUnderlyingConnを直接取得できるため、何もしない
}
