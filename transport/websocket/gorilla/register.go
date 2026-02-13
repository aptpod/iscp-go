package gorilla

import "github.com/aptpod/iscp-go/v2/transport/websocket"

func init() {
	websocket.RegisterDialFunc(DialWithTLS)
}
