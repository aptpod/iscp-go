package nhooyr

import (
	"context"
	"net/http"

	"github.com/aptpod/iscp-go/transport/websocket"
	nwebsocket "nhooyr.io/websocket"
)

// Dialerは、WebSocketのコネクションを開きます。
type Dialer struct{}

// Dialは、WebSocketのコネクションを開きます。
//
// `token` はWebSocket接続時の認証ヘッダーに使用します。
func Dial(wsURL string, tk *websocket.Token) (websocket.Conn, error) {
	var header http.Header
	if tk != nil {
		header = http.Header{}
		header.Add(tk.Header, tk.Token)
	}
	dialOpts := nwebsocket.DialOptions{
		CompressionMode: nwebsocket.CompressionNoContextTakeover,
		HTTPHeader:      header,
	}

	//nolint
	wsconn, _, err := nwebsocket.Dial(context.Background(), wsURL, &dialOpts)
	if err != nil {
		return nil, err
	}
	return New(wsconn), nil
}
