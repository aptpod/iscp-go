package reconnect

import (
	"encoding/binary"
	"fmt"
)

// MessageType represents the type of ping/pong control message.
type MessageType byte

const (
	// MessageTypePing is the ping message type (0x00)
	MessageTypePing MessageType = 0x00
	// MessageTypePong is the pong message type (0x01)
	MessageTypePong MessageType = 0x01
)

const (
	// Magic byte for control messages (0xFF)
	magicByte byte = 0xFF
	// Total message length (fixed 6 bytes)
	messageLength = 6
	// Reserved message type range (0x02-0x0F)
	reservedTypeMin byte = 0x02
	reservedTypeMax byte = 0x0F
)

// PingPongMessage represents a decoded ping or pong message.
type PingPongMessage struct {
	Type     MessageType // Ping or Pong
	Sequence uint32      // Sequence number
}

// MarshalBinary encodes the PingPongMessage into its binary representation.
//
// Returns a 6-byte slice containing the encoded message.
// For Ping: [0xFF, 0x00, seq[0], seq[1], seq[2], seq[3]]
// For Pong: [0xFF, 0x01, seq[0], seq[1], seq[2], seq[3]]
func (m *PingPongMessage) MarshalBinary() ([]byte, error) {
	if m.Type != MessageTypePing && m.Type != MessageTypePong {
		return nil, fmt.Errorf("invalid message type: 0x%02X", m.Type)
	}

	msg := make([]byte, messageLength)
	msg[0] = magicByte
	msg[1] = byte(m.Type)
	binary.BigEndian.PutUint32(msg[2:6], m.Sequence)
	return msg, nil
}

// UnmarshalBinary decodes the binary data into the PingPongMessage.
//
// Returns an error if the data is not a valid ping/pong message.
func (m *PingPongMessage) UnmarshalBinary(data []byte) error {
	msg, isControl, err := ParsePingPong(data)
	if err != nil {
		return err
	}
	if !isControl {
		return fmt.Errorf("not a control message")
	}
	if msg == nil {
		return fmt.Errorf("failed to decode ping/pong message")
	}

	m.Type = msg.Type
	m.Sequence = msg.Sequence
	return nil
}

// EncodePing creates a 6-byte ping message with the given sequence number.
//
// Format: [0xFF, 0x00, seq[0], seq[1], seq[2], seq[3]]
// - Byte 0: Magic byte (0xFF)
// - Byte 1: Type (0x00 for Ping)
// - Bytes 2-5: Sequence number (big-endian uint32)
func EncodePing(seq uint32) []byte {
	msg := make([]byte, messageLength)
	msg[0] = magicByte
	msg[1] = byte(MessageTypePing)
	binary.BigEndian.PutUint32(msg[2:6], seq)
	return msg
}

// EncodePong creates a 6-byte pong message with the given sequence number.
//
// Format: [0xFF, 0x01, seq[0], seq[1], seq[2], seq[3]]
// - Byte 0: Magic byte (0xFF)
// - Byte 1: Type (0x01 for Pong)
// - Bytes 2-5: Sequence number (big-endian uint32)
func EncodePong(seq uint32) []byte {
	msg := make([]byte, messageLength)
	msg[0] = magicByte
	msg[1] = byte(MessageTypePong)
	binary.BigEndian.PutUint32(msg[2:6], seq)
	return msg
}

// ParsePingPong attempts to decode a ping/pong control message.
//
// Returns:
//   - (message, true, nil) if the data is a valid ping/pong message
//   - (nil, false, nil) if the data is not a control message (should be passed to upper layer)
//   - (nil, false, error) if the data is a control message but has a protocol error
//
// Protocol validation:
//   - If byte 0 != 0xFF, it's not a control message (return false)
//   - If byte 1 is in reserved range 0x02-0x0F, it's a protocol error
//   - If byte 1 is not 0x00 or 0x01, it's not a ping/pong (return false)
//   - If length < 6 bytes, it's a protocol error
func ParsePingPong(data []byte) (*PingPongMessage, bool, error) {
	// Check if it's a control message (magic byte)
	if len(data) == 0 || data[0] != magicByte {
		// Not a control message, pass to upper layer
		return nil, false, nil
	}

	// Must have at least 2 bytes to inspect the message type
	if len(data) < 2 {
		return nil, false, fmt.Errorf("control message too short: got %d bytes, expected at least 2", len(data))
	}

	msgType := data[1]

	// Check for reserved message types (protocol error)
	if msgType >= reservedTypeMin && msgType <= reservedTypeMax {
		return nil, false, fmt.Errorf("reserved control message type: 0x%02X", msgType)
	}

	// Check for ping or pong
	if msgType != byte(MessageTypePing) && msgType != byte(MessageTypePong) {
		// Unknown control message type, not a ping/pong
		return nil, false, nil
	}

	// Check total message length
	if len(data) < messageLength {
		return nil, false, fmt.Errorf("ping/pong message too short: got %d bytes, expected %d", len(data), messageLength)
	}

	// Decode sequence number
	seq := binary.BigEndian.Uint32(data[2:6])

	return &PingPongMessage{
		Type:     MessageType(msgType),
		Sequence: seq,
	}, true, nil
}
