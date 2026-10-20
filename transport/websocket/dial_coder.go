package websocket

import (
	"context"
	"net"
	"net/http"

	"github.com/aptpod/iscp-go/v2/log"

	cwebsocket "github.com/coder/websocket"
)

func init() {
	dialFunc = coderDial
}

// coderDial establishes a WebSocket connection using coder/websocket.
func coderDial(c DialConfig) (Conn, error) {
	logger := log.NewStd()

	var header http.Header
	if c.Token != nil {
		header = http.Header{}
		header.Add(c.Token.Header, c.Token.Token)
	}

	// Variable for capturing the TCP connection
	var capturedConn net.Conn

	// Use HTTPTransport if it is specified; otherwise create a new one
	var tr *http.Transport
	if c.HTTPTransport != nil {
		// When HTTPTransport is specified, the TCP connection is already captured by the caller (websocket.Dialer),
		// so it is not done here
		tr = c.HTTPTransport
	} else {
		// For backward compatibility, create a new one as before when HTTPTransport is not specified
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

		// Only when HTTPTransport is not specified, wrap DialContext to capture the TCP connection
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

	// Use c.Context as the base (Background as before if nil). If DialTimeout is
	// set, the earlier of ctx and DialTimeout becomes the upper limit.
	ctx := c.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if c.DialTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.DialTimeout)
		defer cancel()
	}

	logger.Infof(ctx, "coderDial: establishing WebSocket connection (url=%s, timeout=%v)", c.URL, c.DialTimeout)

	//nolint
	wsconn, _, err := cwebsocket.Dial(ctx, c.URL, &dialOpts)
	if err != nil {
		return nil, err
	}

	wsconn.SetReadLimit(-1)
	logger.Infof(ctx, "coderDial: WebSocket connection established")

	// capturedConn is set in the following cases:
	// - When HTTPTransport is not passed: captured in this file
	// - When HTTPTransport is passed: stays nil (use Dialer.GetLastCapturedConn() of the caller)
	return newCoderConn(wsconn, capturedConn), nil
}
