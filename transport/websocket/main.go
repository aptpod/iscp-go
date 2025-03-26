/*
Package websocket は、 WebSocket を使用したトランスポートを提供するパッケージです。
*/
package websocket

import (
	"bytes"
	"sync"
)

const (
	bufferSize = 4096
)

var (
	encodeBufferPool = sync.Pool{
		New: func() any {
			return bytes.NewBuffer(make([]byte, 0, bufferSize))
		},
	}
	decodeBufferPool = sync.Pool{
		New: func() any {
			return bytes.NewBuffer(make([]byte, 0, bufferSize))
		},
	}
)
