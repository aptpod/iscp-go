package websocket

import (
	"time"

	cwebsocket "github.com/coder/websocket"
)

func CallDialFunc(url string, tk *Token) (Conn, error) {
	return dialFunc(DialConfig{
		URL:                url,
		Token:              tk,
		EnableMultipathTCP: true,
	})
}

func CallCoderDial(c DialConfig) (Conn, error) {
	return coderDial(c)
}

func NewCoderConn(wsconn *cwebsocket.Conn) Conn {
	return newCoderConn(wsconn, nil)
}

// ReadTimeout returns the timeout duration of read operations (for testing).
func (t *Transport) ReadTimeout() time.Duration {
	return t.readTimeout
}

// WriteTimeout returns the timeout duration of write operations (for testing).
func (t *Transport) WriteTimeout() time.Duration {
	return t.writeTimeout
}
