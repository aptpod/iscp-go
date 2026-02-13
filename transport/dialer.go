package transport

import (
	"github.com/aptpod/iscp-go/transport/compress"
)

type DialConfig struct {
	Address        string
	CompressConfig compress.Config
	EncodingName   EncodingName

	// Optional
	// For multi transport
	TransportID      TransportID
	TransportGroupID TransportGroupID

	// ハートビートパラメータ
	HeartbeatInterval *int
	HeartbeatTimeout  *int
}

func (c DialConfig) NegotiationParams() NegotiationParams {
	return NegotiationParams{
		Encoding:           c.EncodingName,
		Compress:           c.CompressConfig.Type(),
		CompressLevel:      &c.CompressConfig.Level,
		CompressWindowBits: &c.CompressConfig.WindowBits,
		TransportID:        c.TransportID,
		TransportGroupID:   c.TransportGroupID,
		HeartbeatInterval:  c.HeartbeatInterval,
		HeartbeatTimeout:   c.HeartbeatTimeout,
	}
}

type Dialer interface {
	Dial(DialConfig) (Transport, error)
}

type DialerFunc func(DialConfig) (Transport, error)

func (f DialerFunc) Dial(c DialConfig) (Transport, error) {
	return f(c)
}
