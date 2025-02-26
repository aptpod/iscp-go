package websocket

import (
	"github.com/aptpod/iscp-go/transport/compress"
)

/*
Config は、トランスポートに関する設定です。
*/
type Config struct {
	// Encoding は、内部で使用するエンコーディングです。
	// このフィールドを nil にすることはできません。
	// Encoding wire.Encoding

	// Conn は、WebSocketのコネクションです。
	// このフィールドを nil にすることはできません。
	Conn Conn

	// QueueSize は、トランスポートとメッセージをやり取りする際のメッセージキューの長さです。
	// 0 に設定された場合は、 DefaultQueueSize の値が使用されます。
	QueueSize int

	// CompressConfig は、圧縮に関する設定です。
	CompressConfig compress.Config

	// NegotiationParams は、このトランスポートで事前ネゴシエーションされたパラメーターです。
	NegotiationParams NegotiationParams
}

/*
Config のデフォルト値は以下のように定義されています。
*/
const (
	DefaultQueueSize = 32
)

func (c Config) webSocketConnOrPanic() Conn {
	if c.Conn == nil {
		panic("WebSocketConn should not be nil")
	}
	return c.Conn
}
