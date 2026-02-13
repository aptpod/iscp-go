//go:build !nhooyr && !gorilla

package wire

import (
	_ "github.com/aptpod/iscp-go/v2/transport/websocket/coder"
)
