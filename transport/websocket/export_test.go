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

// ReadTimeoutは、読み込み操作のタイムアウト時間を返却します（テスト用）。
func (t *Transport) ReadTimeout() time.Duration {
	return t.readTimeout
}

// WriteTimeoutは、書き込み操作のタイムアウト時間を返却します（テスト用）。
func (t *Transport) WriteTimeout() time.Duration {
	return t.writeTimeout
}
