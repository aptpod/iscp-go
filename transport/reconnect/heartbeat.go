package reconnect

import (
	"github.com/aptpod/iscp-go/transport/protocol"
)

// MessageType はメッセージの種類を表すバイトです。
// 後方互換性のため transport/protocol パッケージから re-export しています。
type MessageType = protocol.MessageType

const (
	// MessageTypeISCP は iSCP メッセージを表します (0x00)
	MessageTypeISCP = protocol.MessageTypeISCP
	// MessageTypeHeartbeat はハートビートメッセージを表します (0x01)
	MessageTypeHeartbeat = protocol.MessageTypeHeartbeat
)

// HeartbeatMessage はハートビートメッセージを表します。
// 後方互換性のため transport/protocol パッケージから re-export しています。
type HeartbeatMessage = protocol.HeartbeatMessage

// ParseMessageType は先頭バイトからメッセージタイプを判定します。
// 後方互換性のため transport/protocol パッケージから re-export しています。
var ParseMessageType = protocol.ParseMessageType
