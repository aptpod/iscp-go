package websocket

import (
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
