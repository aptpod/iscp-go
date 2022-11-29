/*
Package encoding は、 iSCP で使用するエンコーディングをまとめたパッケージです。
*/
package encoding

//go:generate ./gen_proto.sh

import (
	"bytes"
	"io"
	"sync/atomic"

	"github.com/aptpod/iscp-go/errors"
	"github.com/aptpod/iscp-go/message"
	"github.com/aptpod/iscp-go/transport"
)

//go:generate mockgen -destination ./${GOPACKAGE}mock/${GOFILE} -package ${GOPACKAGE}mock -source ./${GOFILE}

/*
Encoding は、 iSCP のエンコード層を抽象化したインターフェースです。
*/
type Encoding interface {
	// EncodeTo は、 iSCP のメッセージをバイナリへエンコードし、与えられた Writer に書き込みます。
	EncodeTo(io.Writer, message.Message) (int, error)

	// DecodeFrom は、与えられた Reader から読みだしたバイナリを、 iSCP のメッセージへデコードします。
	DecodeFrom(io.Reader) (int, message.Message, error)

	// ContentType は、このエンコーディングの ContentType を返します。
	ContentType() ContentType

	// Name は、このエンコーディングの識別名を返します。
	Name() Name
}

// ContentType は、エンコードされたメッセージの形式を表します。
type ContentType string

const (
	// ContentTypeBinary は、バイナリ形式の EncodingContentType を表します。
	ContentTypeBinary ContentType = "binary"

	// ContentTypeText は、テキスト形式の EncodingContentType を表します。
	ContentTypeText ContentType = "text"
)

// Name は、エンコーディングの識別名を表します。
type Name string

const (
	// NameJSON は、 JSON 形式のエンコーディングを表す名称です。
	NameJSON Name = Name(transport.EncodingNameJSON)

	// NameProtobuf は、 Protocol Buffers 形式のエンコーディングを表す名称です。
	NameProtobuf Name = Name(transport.EncodingNameProtobuf)
)

// TransportConfigは、エンコーディングされたメッセージを伝送するトランスポートについての設定です。
type TransportConfig struct {
	Transport      transport.ReadWriter
	Encoding       Encoding
	MaxMessageSize Size
}

// NewTransportは、エンコーディングされたメッセージを伝送するトランスポートを生成します。
func NewTransport(c *TransportConfig) *Transport {
	return &Transport{
		txCounter:      newCounter(),
		rxCounter:      newCounter(),
		t:              c.Transport,
		e:              c.Encoding,
		maxMessageSize: c.MaxMessageSize,
	}
}

// Transportは、エンコーディングされたメッセージを伝送するトランスポートです。
//
// エンコーディングされたメッセージをトランスポートから読み込んだり、トランスポートへ書き込んだりして使用します。
type Transport struct {
	t              transport.ReadWriter
	e              Encoding
	maxMessageSize Size

	rx, tx    uint64
	txCounter *counter
	rxCounter *counter
}

// Readは、トランスポートからメッセージを読み込みます。
func (c *Transport) Read() (message.Message, error) {
	bs, err := c.t.Read()
	if err != nil {
		return nil, err
	}
	if err := validateMessageSize(c.maxMessageSize, Size(len(bs))); err != nil {
		return nil, err
	}
	read, m, err := c.e.DecodeFrom(bytes.NewBuffer(bs))
	if err != nil {
		return nil, err
	}
	atomic.AddUint64(&c.rx, 1)
	c.rxCounter.Add(m, read)
	return m, nil
}

// RxCountは、トランスポートから読み込んだメッセージのCountを返却します。
func (c *Transport) RxCount() *Count {
	return c.rxCounter.Count()
}

// TxCountは、トランスポートへ書き込んだメッセージのCountを返却します。
func (c *Transport) TxCount() *Count {
	return c.txCounter.Count()
}

// RxMessageCounterValueは、トランスポートから読み込んだメッセージの数を返却します。
func (c *Transport) RxMessageCounterValue() uint64 {
	return atomic.LoadUint64(&c.rx)
}

// Writeは、トランスポートへメッセージを書き出します。
func (c *Transport) Write(message message.Message) error {
	var buf bytes.Buffer
	wrote, err := c.e.EncodeTo(&buf, message)
	if err != nil {
		return err
	}
	if err := c.t.Write(buf.Bytes()); err != nil {
		return err
	}
	atomic.AddUint64(&c.tx, 1)
	c.txCounter.Add(message, wrote)
	return nil
}

// TxMessageCounterValueは、トランスポートへ書き込んだメッセージの数を返却します。
func (c *Transport) TxMessageCounterValue() uint64 {
	return atomic.LoadUint64(&c.tx)
}

// Closeは、トランスポートを閉じます。
func (e *Transport) Close() error {
	return e.t.Close()
}

func validateMessageSize(max Size, target Size) error {
	if max == 0 {
		return nil
	}
	if target > max {
		return errors.Errorf("max_size is %s but got %s: %w", target.String(), max.String(), errors.ErrMessageTooLarge)
	}
	return nil
}
