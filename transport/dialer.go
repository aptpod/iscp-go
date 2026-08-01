package transport

import (
	"context"

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

// ContextDialer は、ctx を尊重して接続を確立できる Dialer です。
//
// 実装は任意です（optional interface）。ctx のキャンセル・期限切れで
// 進行中の接続確立を中断できる Dialer はこのインターフェースを実装して
// ください。実装しない Dialer は DialWithContext 経由では従来どおり
// Dial が呼ばれ、その Dialer 自身のタイムアウト設定だけが上限になります。
type ContextDialer interface {
	DialContext(ctx context.Context, c DialConfig) (Transport, error)
}

// DialWithContext は、d が ContextDialer を実装していれば DialContext を、
// そうでなければ従来の Dial を呼びます。
//
// フォールバック時に Dial を goroutine で包んで ctx で早期 return する
// ことはしません。Dial が返らない限り goroutine と下層リソースが残り、
// リーク源になるためです。
func DialWithContext(ctx context.Context, d Dialer, c DialConfig) (Transport, error) {
	if cd, ok := d.(ContextDialer); ok {
		return cd.DialContext(ctx, c)
	}
	return d.Dial(c)
}
