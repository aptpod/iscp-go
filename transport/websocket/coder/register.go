package coder

import "github.com/aptpod/iscp-go/transport/websocket"

func init() {
	websocket.RegisterDialFunc(DialWithTLS)
}
