package wire

import (
	"github.com/aptpod/iscp-go/message"
	"github.com/aptpod/iscp-go/transport"
)

//go:generate mockgen -destination ./${GOPACKAGE}mock/${GOFILE} -package ${GOPACKAGE}mock -source ./${GOFILE}

// EncodingTransportは、メッセージをエンコーディングし、トランスポートへ読み書きします。
type EncodingTransport interface {
	ReadMessage() (message.Message, error)
	WriteMessage(message message.Message) error
	Close() error
	UnderlyingTransport() transport.ReadWriter
}
