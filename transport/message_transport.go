package transport

import (
	"bytes"
	"io"
	"sync/atomic"

	"github.com/aptpod/iscp-go/v2/errors"
	"github.com/aptpod/iscp-go/v2/message"
)

// ContentType は、エンコードされたメッセージの形式を表します。
type ContentType string

const (
	// ContentTypeBinary は、バイナリ形式の ContentType を表します。
	ContentTypeBinary ContentType = "binary"

	// ContentTypeText は、テキスト形式の ContentType を表します。
	ContentTypeText ContentType = "text"
)

// Encoding は、iSCPメッセージのエンコード/デコードを行うインターフェースです。
type Encoding interface {
	EncodeTo(io.Writer, message.Message) (int, error)
	DecodeFrom(io.Reader) (int, message.Message, error)

	// ContentType は、このエンコーディングの ContentType を返します。
	ContentType() ContentType

	// Name は、このエンコーディングの識別名を返します。
	Name() EncodingName
}

// MessageTransportConfig は、MessageTransportを生成するための設定です。
type MessageTransportConfig struct {
	Transport      ReadWriter
	Encoding       Encoding
	MaxMessageSize int64
}

// MessageTransport は、バイトレベルのトランスポートにエンコーディングを組み合わせて、メッセージレベルのI/Oを提供します。
type MessageTransport struct {
	t              ReadWriter
	encoding       Encoding
	maxMessageSize int64

	rx, tx    uint64
	rxCounter *counter
	txCounter *counter
}

// NewMessageTransport は、新しいMessageTransportを生成します。
func NewMessageTransport(c *MessageTransportConfig) *MessageTransport {
	return &MessageTransport{
		t:              c.Transport,
		encoding:       c.Encoding,
		maxMessageSize: c.MaxMessageSize,
		rxCounter:      newCounter(),
		txCounter:      newCounter(),
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
	n, m, err := mt.encoding.DecodeFrom(bytes.NewBuffer(bs))
	if err != nil {
		return nil, err
	}
	atomic.AddUint64(&mt.rx, 1)
	mt.rxCounter.Add(m, n)
	return m, nil
}

// WriteMessage は、トランスポートへメッセージを書き出します。
func (mt *MessageTransport) WriteMessage(msg message.Message) error {
	em, err := mt.EncodeMessage(msg)
	if err != nil {
		return err
	}
	return mt.WriteEncodedMessage(em)
}

// EncodedMessage は、EncodeMessage で符号化済みのメッセージです。
// WriteEncodedMessage で書き出すまで、符号化結果を保持します。
type EncodedMessage struct {
	msg message.Message
	buf bytes.Buffer
	n   int
}

// EncodeMessage は、msg をトランスポートへの書き込み用に符号化します（書き込みは行いません）。
//
// WriteEncodedMessage と組で使うことで、符号化と書き込みを別々のタイミングで
// 実行できます（例: 書き込み順序を直列化しつつ符号化は並列に行う）。
// 単に書き出すだけなら WriteMessage を使ってください。
func (mt *MessageTransport) EncodeMessage(msg message.Message) (*EncodedMessage, error) {
	em := &EncodedMessage{msg: msg}
	n, err := mt.encoding.EncodeTo(&em.buf, msg)
	if err != nil {
		return nil, err
	}
	em.n = n
	return em, nil
}

// WriteEncodedMessage は、EncodeMessage で符号化済みのメッセージをトランスポートへ書き出します。
//
// 送信カウンタ（TxMessageCount / TxCount）は WriteMessage と同様に、
// 書き込みが成功した時点で加算されます。
func (mt *MessageTransport) WriteEncodedMessage(em *EncodedMessage) error {
	if err := mt.t.Write(em.buf.Bytes()); err != nil {
		return err
	}
	atomic.AddUint64(&mt.tx, 1)
	mt.txCounter.Add(em.msg, em.n)
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

// RxCount はトランスポートから読み込んだメッセージの種別ごとのカウントを返します。
func (mt *MessageTransport) RxCount() *Count {
	return mt.rxCounter.Count()
}

// TxCount はトランスポートへ書き込んだメッセージの種別ごとのカウントを返します。
func (mt *MessageTransport) TxCount() *Count {
	return mt.txCounter.Count()
}
