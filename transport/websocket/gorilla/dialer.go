package gorilla

import (
	"net/http"
	"net/http/httputil"
	"strings"

	"github.com/aptpod/iscp-go/errors"
	"github.com/aptpod/iscp-go/transport/websocket"
	gwebsocket "github.com/gorilla/websocket"
)

// Dialerは、WebSocketのコネクションを開きます。
type Dialer struct{}

// Dialは、WebSocketのコネクションを開きます。
//
// `token` はWebSocket接続時の認証ヘッダーに使用します。
func Dial(wsURL string, token *websocket.Token) (websocket.Conn, error) {
	wsURL = strings.Replace(wsURL, "http", "ws", 1)
	var header http.Header
	if token != nil {
		header = http.Header{}
		header.Add(token.Header, token.Token)
	}
	//nolint
	wsconn, resp, err := gwebsocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		if resp == nil {
			return nil, err
		}

		dump, _ := httputil.DumpResponse(resp, true)
		return nil, errors.Errorf("dial failed with error response[%s]: %w", dump, err)
	}
	return New(wsconn), nil
}
