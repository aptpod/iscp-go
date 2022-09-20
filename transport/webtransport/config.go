package webtransport

import (
	"time"

	"github.com/aptpod/iscp-go/transport/compress"
	webtransgo "github.com/marten-seemann/webtransport-go"
)

/*
Config は、トランスポートに関する設定です。
*/
type Config struct {
	// Connection は、QUICのコネクションです。
	// このフィールドを nil にすることはできません。
	Connection *webtransgo.Conn

	// QueueSize は、トランスポートとメッセージをやり取りする際のメッセージキューの長さです。
	// 0 に設定された場合は、 DefaultQueueSize の値が使用されます。
	QueueSize int

	// CompressConfig は、圧縮に関する設定です。
	CompressConfig compress.Config

	// ReadBufferExpiry は DATAGRAMメッセージのバッファ有効期限です。
	// 0 に設定された場合は、DefaultReadBufferExpiryが使用されます。
	ReadBufferExpiry time.Duration

	// NegotiationParams は、このトランスポートで事前ネゴシエーションされたパラメーターです。
	NegotiationParams NegotiationParams
}

/*
Config のデフォルト値は以下のように定義されています。
*/
const (
	DefaultQueueSize = 32
)

func (c Config) connectionOrPanic() *webtransgo.Conn {
	if c.Connection == nil {
		panic("Connection should not be nil")
	}
	return c.Connection
}

func (c Config) queueSizeOrDefault() int {
	if c.QueueSize == 0 {
		return DefaultQueueSize
	}
	return c.QueueSize
}
