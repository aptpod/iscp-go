package transport

import (
	"bytes"
	"io"
	"sync/atomic"

	"github.com/aptpod/iscp-go/errors"
	"github.com/aptpod/iscp-go/message"
)

// ContentType は、エンコードされたメッセージの形式を表します。
type ContentType string

const (
	// ContentTypeBinary は、バイナリ形式の ContentType を表します。
	ContentTypeBinary ContentType = "binary"

	// ContentTypeText は、テキスト形式の ContentType を表します。
	ContentTypeText ContentType = "text"
)

// Codec は、iSCPメッセージのエンコード/デコードを行うインターフェースです。
type Codec interface {
	EncodeTo(io.Writer, message.Message) (int, error)
	DecodeFrom(io.Reader) (int, message.Message, error)

	// ContentType は、このコーデックの ContentType を返します。
	ContentType() ContentType

	// Name は、このコーデックの識別名を返します。
	Name() EncodingName
}

// MessageTransportConfig は、MessageTransportを生成するための設定です。
type MessageTransportConfig struct {
	Transport      ReadWriter
	Codec          Codec
	MaxMessageSize int64
}

// MessageTransport は、バイトレベルのトランスポートにコーデックを組み合わせて、メッセージレベルのI/Oを提供します。
type MessageTransport struct {
	t              ReadWriter
	codec          Codec
	maxMessageSize int64

	rx, tx uint64
}

// NewMessageTransport は、新しいMessageTransportを生成します。
func NewMessageTransport(c *MessageTransportConfig) *MessageTransport {
	return &MessageTransport{
		t:              c.Transport,
		codec:          c.Codec,
		maxMessageSize: c.MaxMessageSize,
	}
}

// ReadMessage は、トランスポートからメッセージを読み込みます。
func (mt *MessageTransport) ReadMessage() (message.Message, error) {
	bs, err := mt.t.Read()
	if err != nil {
		return nil, err
	}
	if mt.maxMessageSize > 0 && int64(len(bs)) > mt.maxMessageSize {
		return nil, errors.Errorf("message too large: %d bytes exceeds max %d: %w", len(bs), mt.maxMessageSize, errors.ErrMessageTooLarge)
	}
	_, m, err := mt.codec.DecodeFrom(bytes.NewBuffer(bs))
	if err != nil {
		return nil, err
	}
	atomic.AddUint64(&mt.rx, 1)
	return m, nil
}

// WriteMessage は、トランスポートへメッセージを書き出します。
func (mt *MessageTransport) WriteMessage(msg message.Message) error {
	var buf bytes.Buffer
	if _, err := mt.codec.EncodeTo(&buf, msg); err != nil {
		return err
	}
	if err := mt.t.Write(buf.Bytes()); err != nil {
		return err
	}
	atomic.AddUint64(&mt.tx, 1)
	return nil
}

// Close は、トランスポートを閉じます。
func (mt *MessageTransport) Close() error {
	return mt.t.Close()
}

// UnderlyingTransport は、内部で使用しているバイトレベルのトランスポートを返します。
func (mt *MessageTransport) UnderlyingTransport() ReadWriter {
	return mt.t
}

// RxMessageCount は、受信したメッセージの数を返します。
func (mt *MessageTransport) RxMessageCount() uint64 {
	return atomic.LoadUint64(&mt.rx)
}

// TxMessageCount は、送信したメッセージの数を返します。
func (mt *MessageTransport) TxMessageCount() uint64 {
	return atomic.LoadUint64(&mt.tx)
}
