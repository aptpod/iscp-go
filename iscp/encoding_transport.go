package iscp

import (
	"github.com/aptpod/iscp-go/message"
	"github.com/aptpod/iscp-go/transport"
)

// EncodingTransportは、メッセージをエンコーディングし、トランスポートへ読み書きします。
type EncodingTransport interface {
	ReadMessage() (message.Message, error)
	WriteMessage(message message.Message) error
	Close() error
	UnderlyingTransport() transport.ReadWriter
}
