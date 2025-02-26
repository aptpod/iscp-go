/*
Package transport は、 iSCP で使用するトランスポートをまとめたパッケージです。
*/
package transport

import (
	"io"
	"time"

	"github.com/aptpod/iscp-go/errors"
)

// Nameは、トランスポート名です。
type Name string

const (
	// QUICトランスポート
	NameQUIC Name = "quic"
	// WebSocketトランスポート
	NameWebSocket Name = "websocket"
	// WebTransportトランスポート
	NameWebTransport Name = "webtransport"

	// マルチコネクションのトランスポート
	NameMulti Name = "multi"
)

// Now は transport内で利用する現在時刻関数です。
var Now = time.Now

/*
Conn は以下のエラーを返します。
*/
var (
	// ErrAlreadyClosed は、トランスポート層のコネクションが切れている場合に返されます。
	//
	// DEPRECATED: errors.ErrConnectionClosed を使用して下さい
	ErrAlreadyClosed = errors.ErrConnectionClosed

	// ErrInvalidMessage は、 メッセージが不正だった時に返されます。
	//
	// DEPRECATED: errors.ErrMalformedMessage を使用して下さい
	ErrInvalidMessage = errors.ErrMalformedMessage

	EOF = io.EOF
)

type (
	TransportID      string
	TransportGroupID string
)
