package nhooyr

import (
	"context"
	"net"
	"net/http"

	nwebsocket "nhooyr.io/websocket"

	"github.com/aptpod/iscp-go/transport/websocket"
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

	// TCP接続をキャプチャするための変数
	var capturedConn net.Conn

	var cli http.Client
	if c.TLSConfig != nil {
		cli.Transport = http.DefaultTransport
		cli.Transport.(*http.Transport).TLSClientConfig = c.TLSConfig
	}

	// DialContextを設定（TCP接続のキャプチャを含む）
	var baseDialContext func(ctx context.Context, network, addr string) (net.Conn, error)

	if c.EnableMultipathTCP {
		dialer := net.Dialer{}
		dialer.SetMultipathTCP(c.EnableMultipathTCP)
		cli.Transport = http.DefaultTransport
		baseDialContext = dialer.DialContext
	}

	if c.DialContext != nil {
		if cli.Transport == nil {
			cli.Transport = http.DefaultTransport
		}
		baseDialContext = c.DialContext
	}

	// baseDialContextがnilの場合はデフォルトのダイアラーを使用
	if baseDialContext == nil {
		baseDialContext = (&net.Dialer{}).DialContext
	}

	// 必ずDialContextをラップしてTCP接続をキャプチャ
	if cli.Transport == nil {
		cli.Transport = http.DefaultTransport
	}
	cli.Transport.(*http.Transport).DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := baseDialContext(ctx, network, addr)
		if err == nil {
			capturedConn = conn
		}
		return conn, err
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

	// TCP接続は必ずキャプチャされている（DialContextが必ず呼ばれるため）
	return NewWithUnderlyingConn(wsconn, capturedConn), nil
}
