package protocol

import (
	"fmt"
)

// MessageType はトランスポートレベルのメッセージタイプを表します。
type MessageType byte

const (
	// MessageTypeISCP は iSCP メッセージを表します (0x00)
	MessageTypeISCP MessageType = 0x00
	// MessageTypeHeartbeat はハートビートメッセージを表します (0x01)
	MessageTypeHeartbeat MessageType = 0x01
)

// HeartbeatMessage はハートビートメッセージを表します。
// ハートビートは1バイトの 0x01 で構成されます。
type HeartbeatMessage struct{}

// MarshalBinary はハートビートメッセージをバイナリにエンコードします。
func (m *HeartbeatMessage) MarshalBinary() ([]byte, error) {
	return []byte{byte(MessageTypeHeartbeat)}, nil
}

// ParseMessageType は先頭バイトからメッセージタイプを判定します。
// 0x00 = iSCPメッセージ, 0x01 = ハートビート, 0x02-0xFF = プロトコルエラー
func ParseMessageType(data []byte) (MessageType, error) {
	if len(data) == 0 {
		return 0, fmt.Errorf("empty message")
	}
	msgType := MessageType(data[0])
	switch msgType {
	case MessageTypeISCP:
		return MessageTypeISCP, nil
	case MessageTypeHeartbeat:
		return MessageTypeHeartbeat, nil
	default:
		return 0, fmt.Errorf("unknown message type: 0x%02X", data[0])
	}
}
