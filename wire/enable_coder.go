//go:build !nhooyr && !gorilla

package wire

import (
	_ "github.com/aptpod/iscp-go/transport/websocket/coder"
)
