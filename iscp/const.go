package iscp

import (
	"github.com/aptpod/iscp-go/encoding"
	"github.com/aptpod/iscp-go/encoding/json"
	"github.com/aptpod/iscp-go/encoding/protobuf"
	"github.com/aptpod/iscp-go/transport"
)

// TransportNameは、トランスポート名です。
type TransportName string

const (
	// QUICトランスポート
	TransportNameQUIC TransportName = TransportName(transport.NameQUIC)
	// WebSocketトランスポート
	TransportNameWebSocket TransportName = TransportName(transport.NameWebSocket)
	// WebTransportトランスポート
	TransportNameWebTransport TransportName = TransportName(transport.NameWebTransport)

	// マルチコネクションのトランスポート
	TransportNameMulti TransportName = TransportName(transport.NameMulti)
)

// EncodingNameは、エンコーディング名です。
type EncodingName string

const (
	// Protobufエンコーディング
	EncodingNameProtobuf EncodingName = EncodingName(encoding.NameProtobuf)
	// JSONエンコーディング
	EncodingNameJSON EncodingName = EncodingName(encoding.NameJSON)
)

func (e EncodingName) toEncoding() encoding.Encoding {
	switch e {
	case EncodingNameProtobuf:
		return protobuf.NewEncoding()
	case EncodingNameJSON:
		return json.NewEncoding()
	default:
		return protobuf.NewEncoding()
	}
}
