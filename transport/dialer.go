package transport

import (
	"github.com/aptpod/iscp-go/transport/compress"
)

type DialConfig struct {
	Address        string
	CompressConfig compress.Config
	EncodingName   EncodingName
}

func (c DialConfig) NegotiationParams() NegotiationParams {
	return NegotiationParams{
		Encoding:           c.EncodingName,
		Compress:           c.CompressConfig.Type(),
		CompressLevel:      &c.CompressConfig.Level,
		CompressWindowBits: &c.CompressConfig.WindowBits,
	}
}

type Dialer interface {
	Dial(DialConfig) (Transport, error)
}
