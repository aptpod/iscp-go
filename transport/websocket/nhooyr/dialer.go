package nhooyr

import (
	"context"
	"crypto/tls"
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
	return DialWithTLS(wsURL, tk, nil)
}

// DialWithTLSは、WebSocketのコネクションを開きます。
//
// `token` はWebSocket接続時の認証ヘッダーに使用します。
// `tlsConfig` がnilの場合は無視します。
func DialWithTLS(wsURL string, tk *websocket.Token, tlsConfig *tls.Config) (websocket.Conn, error) {
	var header http.Header
	if tk != nil {
		header = http.Header{}
		header.Add(tk.Header, tk.Token)
	}
	var cli http.Client
	if tlsConfig != nil {
		cli.Transport = &http.Transport{
			TLSClientConfig: tlsConfig,
		}
	}
	dialOpts := nwebsocket.DialOptions{
		CompressionMode: nwebsocket.CompressionNoContextTakeover,
		HTTPHeader:      header,
		HTTPClient:      &cli,
	}

	//nolint
	wsconn, _, err := nwebsocket.Dial(context.Background(), wsURL, &dialOpts)
	if err != nil {
		return nil, err
	}
	return New(wsconn), nil
}
