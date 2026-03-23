package websocket

import (
	"context"
	"net"
	"net/http"

	"github.com/aptpod/iscp-go/log"

	cwebsocket "github.com/coder/websocket"
)

func init() {
	dialFunc = coderDial
}

// coderDialは、coder/websocketを使用してWebSocket接続を確立します。
func coderDial(c DialConfig) (Conn, error) {
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

	logger.Infof(context.Background(), "coderDial: establishing WebSocket connection (url=%s, timeout=%v)", c.URL, c.DialTimeout)

	//nolint
	wsconn, _, err := cwebsocket.Dial(ctx, c.URL, &dialOpts)
	if err != nil {
		return nil, err
	}

	wsconn.SetReadLimit(-1)
	logger.Infof(context.Background(), "coderDial: WebSocket connection established")

	// capturedConnは以下の場合に設定される:
	// - HTTPTransportが渡されていない場合: このファイル内でキャプチャ
	// - HTTPTransportが渡された場合: nilのまま（呼び出し側のDialer.GetLastCapturedConn()を使用）
	return newCoderConn(wsconn, capturedConn), nil
}
