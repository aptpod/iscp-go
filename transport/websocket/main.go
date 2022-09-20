/*
Package websocket は、 WebSocket を使用したトランスポートを提供するパッケージです。
*/
package websocket

import (
	"bytes"
	"sync"
)

/*
Name は、本トランスポートの名称です。
*/
const Name = "websocket"

const (
	bufferSize = 4096
)

var bufferPool = sync.Pool{New: func() interface{} {
	return bytes.NewBuffer(make([]byte, 0, bufferSize))
}}
