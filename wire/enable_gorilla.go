//go:build !nhooyr

package wire

import (
	_ "github.com/aptpod/iscp-go/transport/websocket/gorilla"
)
