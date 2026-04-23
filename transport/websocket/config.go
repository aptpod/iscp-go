package websocket

import (
	"time"

	"github.com/aptpod/iscp-go/v2/transport"
	"github.com/aptpod/iscp-go/v2/transport/compress"
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
	NegotiationParams transport.NegotiationParams

	// ReadTimeout は、読み込み操作のタイムアウト時間です。
	// 0 に設定された場合は、 DefaultReadTimeout の値が使用されます。
	ReadTimeout time.Duration

	// WriteTimeout は、書き込み操作のタイムアウト時間です。
	// 0 に設定された場合は、 DefaultWriteTimeout の値が使用されます。
	WriteTimeout time.Duration

	// UseMessageFraming は、メッセージフレーミング（4バイト長プレフィクス + チャンク分割）を有効にします。
	// true (v2, trans=ws2): 4バイトBE長プレフィクス + 最大8KBチャンク分割
	// false (v1): 1 WebSocketメッセージ = 1 iSCPメッセージ
	UseMessageFraming bool
}

/*
Config のデフォルト値は以下のように定義されています。
*/
const (
	DefaultQueueSize    = 32
	DefaultReadTimeout  = 30 * time.Second
	DefaultWriteTimeout = 5 * time.Second
)

func (c Config) webSocketConnOrPanic() Conn {
	if c.Conn == nil {
		panic("WebSocketConn should not be nil")
	}
	return c.Conn
}
