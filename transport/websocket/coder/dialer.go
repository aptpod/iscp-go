package coder

import (
	"context"
	"net"
	"net/http"

	"github.com/aptpod/iscp-go/transport/websocket"

	cwebsocket "github.com/coder/websocket"
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
// DEPRECATED: 代わりに `DialConfig` を使用してください。
func DialWithTLS(c websocket.DialConfig) (websocket.Conn, error) {
	return DialConfig(c)
}

// DialCONFIGは、WebSocketのコネクションを開きます。
//
// `tlsConfig` がnilの場合は無視します。
func DialConfig(c websocket.DialConfig) (websocket.Conn, error) {
	var header http.Header
	if c.Token != nil {
		header = http.Header{}
		header.Add(c.Token.Header, c.Token.Token)
	}

	var cli http.Client
	cli.Transport = http.DefaultTransport.(*http.Transport).Clone()
	if c.TLSConfig != nil {
		cli.Transport.(*http.Transport).TLSClientConfig = c.TLSConfig
	}

	if c.EnableMultipathTCP {
		dialer := net.Dialer{}
		dialer.SetMultipathTCP(c.EnableMultipathTCP)
		cli.Transport.(*http.Transport).DialContext = dialer.DialContext
	}

	if c.DialContext != nil {
		cli.Transport.(*http.Transport).DialContext = c.DialContext
	}
	if c.DialTLSContext != nil {
		cli.Transport.(*http.Transport).DialTLSContext = c.DialTLSContext
	}

	if c.Proxy != nil {
		cli.Transport.(*http.Transport).Proxy = c.Proxy
	}

	dialOpts := cwebsocket.DialOptions{
		CompressionMode: cwebsocket.CompressionNoContextTakeover,
		HTTPHeader:      header,
		HTTPClient:      &cli,
	}

	//nolint
	wsconn, _, err := cwebsocket.Dial(context.Background(), c.URL, &dialOpts)
	if err != nil {
		return nil, err
	}
	wsconn.SetReadLimit(-1)
	return New(wsconn), nil
}
