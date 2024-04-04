package nhooyr

import (
	"context"
	"net"
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
	return DialWithTLS(websocket.DialConfig{
		URL:   wsURL,
		Token: tk,
	})
}

// DialWithTLSは、WebSocketのコネクションを開きます。
//
// `token` はWebSocket接続時の認証ヘッダーに使用します。
// `tlsConfig` がnilの場合は無視します。
func DialWithTLS(c websocket.DialConfig) (websocket.Conn, error) {
	var header http.Header
	if c.Token != nil {
		header = http.Header{}
		header.Add(c.Token.Header, c.Token.Token)
	}
	var cli http.Client
	if c.TLSConfig != nil {
		cli.Transport = http.DefaultTransport
		cli.Transport.(*http.Transport).TLSClientConfig = c.TLSConfig
	}
	if c.EnableMultipathTCP {
		dialer := net.Dialer{}
		dialer.SetMultipathTCP(c.EnableMultipathTCP)
		cli.Transport = http.DefaultTransport
		cli.Transport.(*http.Transport).DialContext = dialer.DialContext
	}
	dialOpts := nwebsocket.DialOptions{
		CompressionMode: nwebsocket.CompressionNoContextTakeover,
		HTTPHeader:      header,
		HTTPClient:      &cli,
	}

	//nolint
	wsconn, _, err := nwebsocket.Dial(context.Background(), c.URL, &dialOpts)
	if err != nil {
		return nil, err
	}
	return New(wsconn), nil
}
