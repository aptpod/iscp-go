package iscp

import (
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
type EncodingName = transport.EncodingName

const (
	// Protobufエンコーディング
	EncodingNameProtobuf EncodingName = transport.EncodingNameProtobuf
	// JSONエンコーディング
	EncodingNameJSON EncodingName = transport.EncodingNameJSON
)

