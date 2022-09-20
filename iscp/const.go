package iscp

import (
	"github.com/aptpod/iscp-go/encoding"
	"github.com/aptpod/iscp-go/encoding/json"
	"github.com/aptpod/iscp-go/encoding/protobuf"
)

// Transportは、トランスポートです。
type Transport string

const (
	// QUICトランスポート
	TransportQUIC Transport = "quic"
	// WebSocketトランスポート
	TransportWebSocket Transport = "websocket"
	// WebTransportトランスポート
	TransportWebTransport Transport = "webtransport"
)

// Encodingは、Encodingです。
type Encoding string

const (
	// Protobufエンコーディング
	EncodingProtobuf Encoding = Encoding(encoding.NameProtobuf)
	// JSONエンコーディング
	EncodingJSON Encoding = Encoding(encoding.NameJSON)
)

func (e Encoding) toEncoding() encoding.Encoding {
	switch e {
	case EncodingProtobuf:
		return protobuf.NewEncoding()
	case EncodingJSON:
		return json.NewEncoding()
	default:
		return protobuf.NewEncoding()
	}
}
