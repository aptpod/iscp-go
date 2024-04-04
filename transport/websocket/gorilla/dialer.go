package gorilla

import (
	"net"
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
	return DialWithTLS(websocket.DialConfig{
		URL:   wsURL,
		Token: token,
	})
}

// DialWithTLSは、WebSocketのコネクションを開きます。
//
// `token` はWebSocket接続時の認証ヘッダーに使用します。
// `tlsConfig` がnilの場合は無視します。
func DialWithTLS(c websocket.DialConfig) (websocket.Conn, error) {
	wsURL := strings.Replace(c.URL, "http", "ws", 1)
	var header http.Header
	if c.Token != nil {
		header = http.Header{}
		header.Add(c.Token.Header, c.Token.Token)
	}
	dd := *gwebsocket.DefaultDialer
	if c.TLSConfig != nil {
		dd.TLSClientConfig = c.TLSConfig
	}
	dialer := net.Dialer{}
	dialer.SetMultipathTCP(c.EnableMultipathTCP)
	dd.NetDialContext = dialer.DialContext

	//nolint
	wsconn, resp, err := dd.Dial(wsURL, header)
	if err != nil {
		if resp == nil {
			return nil, err
		}

		dump, _ := httputil.DumpResponse(resp, true)
		return nil, errors.Errorf("dial failed with error response[%s]: %w", dump, err)
	}
	return New(wsconn), nil
}
