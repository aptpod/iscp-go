package wire

import (
	"github.com/aptpod/iscp-go/encoding"
	"github.com/aptpod/iscp-go/message"
)

//go:generate mockgen -destination ./${GOPACKAGE}mock/${GOFILE} -package ${GOPACKAGE}mock -source ./${GOFILE}

// EncodingTransportは、メッセージをエンコーディングし、トランスポートへ読み書きします。
type EncodingTransport interface {
	Read() (message.Message, error)
	RxCount() *encoding.Count
	RxMessageCounterValue() uint64
	Write(message message.Message) error
	TxMessageCounterValue() uint64
	TxCount() *encoding.Count
	Close() error
}
