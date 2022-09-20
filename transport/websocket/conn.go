package websocket

import (
	"context"
	"io"
)

// MessageTypeは、WebSocketのメッセージタイプを表します。
type MessageType int

const (
	MessageText   MessageType = iota + 1 // テキストメッセージ
	MessageBinary                        // バイナリメッセージ
)

// Connは、WebSocketのコネクションインターフェースです。
type Conn interface {
	// Closeは、コネクションをクローズします。
	Close() error

	// Pingは、Pingを送信します。
	Ping(context.Context) error

	// Readerは、WebSocketメッセージのReaderを返却します。
	Reader(context.Context) (MessageType, io.Reader, error)

	// Writerは、WebSocketメッセージのWriterを返却します。
	Writer(context.Context, MessageType) (io.WriteCloser, error)
}
