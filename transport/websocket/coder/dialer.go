package coder

import (
	"context"
	"net"
	"net/http"

	"github.com/aptpod/iscp-go/log"
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
	logger := log.NewStd()

	var header http.Header
	if c.Token != nil {
		header = http.Header{}
		header.Add(c.Token.Header, c.Token.Token)
	}

	// TCP接続をキャプチャするための変数
	var capturedConn net.Conn

	var cli http.Client
	cli.Transport = http.DefaultTransport.(*http.Transport).Clone()
	if c.TLSConfig != nil {
		cli.Transport.(*http.Transport).TLSClientConfig = c.TLSConfig
	}

	// DialContextを設定（TCP接続のキャプチャを含む）
	var baseDialContext func(ctx context.Context, network, addr string) (net.Conn, error)

	if c.EnableMultipathTCP {
		dialer := net.Dialer{}
		dialer.SetMultipathTCP(c.EnableMultipathTCP)
		baseDialContext = dialer.DialContext
	}

	if c.DialContext != nil {
		baseDialContext = c.DialContext
	}

	// baseDialContextがnilの場合はデフォルトのダイアラーを使用
	if baseDialContext == nil {
		baseDialContext = (&net.Dialer{}).DialContext
	}

	// 必ずDialContextをラップしてTCP接続をキャプチャ
	cli.Transport.(*http.Transport).DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := baseDialContext(ctx, network, addr)
		if err == nil {
			capturedConn = conn
		}
		return conn, err
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

	ctx := context.Background()
	if c.DialTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), c.DialTimeout)
		defer cancel()
	}

	logger.Infof(context.Background(), "DialConfig: establishing WebSocket connection (url=%s, timeout=%v)", c.URL, c.DialTimeout)

	//nolint
	wsconn, _, err := cwebsocket.Dial(ctx, c.URL, &dialOpts)
	if err != nil {
		return nil, err
	}

	wsconn.SetReadLimit(-1)
	logger.Infof(context.Background(), "DialConfig: WebSocket connection established")

	// TCP接続は必ずキャプチャされている（DialContextが必ず呼ばれるため）
	return NewWithUnderlyingConn(wsconn, capturedConn), nil
}
