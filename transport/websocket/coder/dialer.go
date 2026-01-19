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

	// HTTPTransportが指定されている場合はそれを使用し、そうでない場合は新規作成する
	var tr *http.Transport
	if c.HTTPTransport != nil {
		// HTTPTransportが指定された場合、TCP接続のキャプチャは呼び出し側（websocket.Dialer）で
		// 既に行われているため、ここでは行わない
		tr = c.HTTPTransport
	} else {
		// 後方互換性のため、HTTPTransportが指定されていない場合は従来通り新規作成
		tr = http.DefaultTransport.(*http.Transport).Clone()
		if c.TLSConfig != nil {
			tr.TLSClientConfig = c.TLSConfig
		}

		if c.EnableMultipathTCP {
			dialer := net.Dialer{}
			dialer.SetMultipathTCP(c.EnableMultipathTCP)
			tr.DialContext = dialer.DialContext
		}

		if c.DialContext != nil {
			tr.DialContext = c.DialContext
		}
		if c.DialTLSContext != nil {
			tr.DialTLSContext = c.DialTLSContext
		}

		if c.Proxy != nil {
			tr.Proxy = c.Proxy
		}

		// HTTPTransportが指定されていない場合のみ、DialContextをラップしてTCP接続をキャプチャする
		baseDialContext := tr.DialContext
		if baseDialContext == nil {
			baseDialContext = (&net.Dialer{}).DialContext
		}
		tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := baseDialContext(ctx, network, addr)
			if err == nil {
				capturedConn = conn
			}
			return conn, err
		}
	}

	cli := http.Client{
		Transport: tr,
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

	// capturedConnは以下の場合に設定される:
	// - HTTPTransportが渡されていない場合: このファイル内でキャプチャ
	// - HTTPTransportが渡された場合: nilのまま（呼び出し側のDialer.GetLastCapturedConn()を使用）
	return NewWithUnderlyingConn(wsconn, capturedConn), nil
}
