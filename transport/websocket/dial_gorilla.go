package websocket

import (
	"context"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"

	gwebsocket "github.com/gorilla/websocket"

	"github.com/aptpod/iscp-go/v2/errors"
)

// GorillaDial は gorilla/websocket を使用して WebSocket 接続を確立します。
//
// DialerConfig.DialFunc に指定して使用します:
//
//	d := websocket.NewDialer(websocket.DialerConfig{
//	    DialFunc: websocket.GorillaDial,
//	})
func GorillaDial(c DialConfig) (Conn, error) {
	wsURL := strings.Replace(c.URL, "http", "ws", 1)
	var header http.Header
	if c.Token != nil {
		header = http.Header{}
		header.Add(c.Token.Header, c.Token.Token)
	}
	dd := *gwebsocket.DefaultDialer

	// c.Context を尊重する（nil なら Background）。あわせて HandshakeTimeout を
	// DefaultDialer の 45 秒から c.DialTimeout（DialConfig の契約どおり 0 なら
	// 無制限）に差し替える。従来は c.DialTimeout を無視して常に 45 秒だった。
	ctx := c.Context
	if ctx == nil {
		ctx = context.Background()
	}
	dd.HandshakeTimeout = c.DialTimeout

	// HTTPTransportが指定されている場合はそれを使用
	if c.HTTPTransport != nil {
		dd.TLSClientConfig = c.HTTPTransport.TLSClientConfig
		dd.NetDialContext = c.HTTPTransport.DialContext
		dd.Proxy = c.HTTPTransport.Proxy
	} else {
		if c.TLSConfig != nil {
			dd.TLSClientConfig = c.TLSConfig
		}
		dialer := net.Dialer{}
		dialer.SetMultipathTCP(c.EnableMultipathTCP)
		dd.NetDialContext = dialer.DialContext
		if c.Proxy != nil {
			dd.Proxy = c.Proxy
		}
	}

	//nolint
	wsconn, resp, err := dd.DialContext(ctx, wsURL, header)
	if err != nil {
		if resp == nil {
			return nil, err
		}

		dump, _ := httputil.DumpResponse(resp, true)
		return nil, errors.Errorf("dial failed with error response[%s]: %w", dump, err)
	}
	return newGorillaConn(wsconn), nil
}
