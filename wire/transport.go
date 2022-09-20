package wire

import (
	"github.com/aptpod/iscp-go/message"
)

//go:generate mockgen -destination ./${GOPACKAGE}mock/${GOFILE} -package ${GOPACKAGE}mock -source ./${GOFILE}
type Transport interface {
	Read() (message.Message, error)
	RxMessageCounterValue() uint64
	Write(message message.Message) error
	TxMessageCounterValue() uint64
	Close() error
}
