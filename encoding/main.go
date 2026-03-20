/*
Package encoding は、 iSCP で使用するエンコーディングをまとめたパッケージです。
*/
package encoding

import (
	"io"

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
type Name = transport.EncodingName

const (
	// NameJSON は、 JSON 形式のエンコーディングを表す名称です。
	NameJSON Name = transport.EncodingNameJSON

	// NameProtobuf は、 Protocol Buffers 形式のエンコーディングを表す名称です。
	NameProtobuf Name = transport.EncodingNameProtobuf
)
