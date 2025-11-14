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

// PingMessage represents a decoded ping control message.
type PingMessage struct {
	Sequence uint32 // Sequence number
}

// PongMessage represents a decoded pong control message.
type PongMessage struct {
	Sequence uint32 // Sequence number
}

// MarshalBinary encodes the PingMessage into its binary representation.
//
// Returns a 6-byte slice containing the encoded message.
// Format: [0xFF, 0x00, seq[0], seq[1], seq[2], seq[3]]
func (m *PingMessage) MarshalBinary() ([]byte, error) {
	msg := make([]byte, messageLength)
	msg[0] = magicByte
	msg[1] = byte(MessageTypePing)
	binary.BigEndian.PutUint32(msg[2:6], m.Sequence)
	return msg, nil
}

// MarshalBinary encodes the PongMessage into its binary representation.
//
// Returns a 6-byte slice containing the encoded message.
// Format: [0xFF, 0x01, seq[0], seq[1], seq[2], seq[3]]
func (m *PongMessage) MarshalBinary() ([]byte, error) {
	msg := make([]byte, messageLength)
	msg[0] = magicByte
	msg[1] = byte(MessageTypePong)
	binary.BigEndian.PutUint32(msg[2:6], m.Sequence)
	return msg, nil
}

// UnmarshalBinary decodes the binary data into the PingMessage.
//
// Returns an error if the data is not a valid ping message.
func (m *PingMessage) UnmarshalBinary(data []byte) error {
	ping, ok, err := TryParsePing(data)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("not a ping message")
	}
	m.Sequence = ping.Sequence
	return nil
}

// UnmarshalBinary decodes the binary data into the PongMessage.
//
// Returns an error if the data is not a valid pong message.
func (m *PongMessage) UnmarshalBinary(data []byte) error {
	pong, ok, err := TryParsePong(data)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("not a pong message")
	}
	m.Sequence = pong.Sequence
	return nil
}

// TryParsePing attempts to decode a ping control message.
//
// Returns:
//   - (message, true, nil) if the data is a valid ping message
//   - (nil, false, nil) if the data is not a ping message (could be pong or regular data)
//   - (nil, false, error) if the data is a control message but has a protocol error
//
// Protocol validation:
//   - If byte 0 != 0xFF, it's not a control message (return false)
//   - If byte 1 is in reserved range 0x02-0x0F, it's a protocol error
//   - If byte 1 is not 0x00 (ping), it's not a ping (return false)
//   - If length < 6 bytes, it's a protocol error
func TryParsePing(data []byte) (*PingMessage, bool, error) {
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

	// Check if it's a ping
	if msgType != byte(MessageTypePing) {
		// Not a ping message (could be pong or other control message)
		return nil, false, nil
	}

	// Check total message length
	if len(data) < messageLength {
		return nil, false, fmt.Errorf("ping message too short: got %d bytes, expected %d", len(data), messageLength)
	}

	// Decode sequence number
	seq := binary.BigEndian.Uint32(data[2:6])

	return &PingMessage{
		Sequence: seq,
	}, true, nil
}

// TryParsePong attempts to decode a pong control message.
//
// Returns:
//   - (message, true, nil) if the data is a valid pong message
//   - (nil, false, nil) if the data is not a pong message (could be ping or regular data)
//   - (nil, false, error) if the data is a control message but has a protocol error
//
// Protocol validation:
//   - If byte 0 != 0xFF, it's not a control message (return false)
//   - If byte 1 is in reserved range 0x02-0x0F, it's a protocol error
//   - If byte 1 is not 0x01 (pong), it's not a pong (return false)
//   - If length < 6 bytes, it's a protocol error
func TryParsePong(data []byte) (*PongMessage, bool, error) {
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

	// Check if it's a pong
	if msgType != byte(MessageTypePong) {
		// Not a pong message (could be ping or other control message)
		return nil, false, nil
	}

	// Check total message length
	if len(data) < messageLength {
		return nil, false, fmt.Errorf("pong message too short: got %d bytes, expected %d", len(data), messageLength)
	}

	// Decode sequence number
	seq := binary.BigEndian.Uint32(data[2:6])

	return &PongMessage{
		Sequence: seq,
	}, true, nil
}
