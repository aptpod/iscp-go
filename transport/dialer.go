package transport

import (
	"github.com/aptpod/iscp-go/v2/transport/compress"
)

type DialConfig struct {
	Address        string
	CompressConfig compress.Config
	EncodingName   EncodingName

	// Optional
	// For multi transport
	SubConnectionID   SubConnectionID
	SuperConnectionID SuperConnectionID

	// トランスポート種別（ws2, quic2, webtrans2）
	TransportType Name

	// 再接続パラメータ
	MaxReconnectAttempts *int
	ReconnectInterval    *int

	// ハートビートパラメータ
	HeartbeatInterval *int
	HeartbeatTimeout  *int
}

func (c DialConfig) NegotiationParams() NegotiationParams {
	return NegotiationParams{
		Encoding:             c.EncodingName,
		Compress:             c.CompressConfig.Type(),
		CompressLevel:        &c.CompressConfig.Level,
		CompressWindowBits:   &c.CompressConfig.WindowBits,
		SubConnectionID:      c.SubConnectionID,
		SuperConnectionID:    c.SuperConnectionID,
		TransportType:        c.TransportType,
		MaxReconnectAttempts: c.MaxReconnectAttempts,
		ReconnectInterval:    c.ReconnectInterval,
		HeartbeatInterval:    c.HeartbeatInterval,
		HeartbeatTimeout:     c.HeartbeatTimeout,
	}
}

type Dialer interface {
	Dial(DialConfig) (Transport, error)
}

type DialerFunc func(DialConfig) (Transport, error)

func (f DialerFunc) Dial(c DialConfig) (Transport, error) {
	return f(c)
}
