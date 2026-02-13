package reconnect

import (
	"testing"
)

func TestHeartbeatMessage_MarshalBinary(t *testing.T) {
	msg := &HeartbeatMessage{}
	data, err := msg.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}
	if len(data) != 1 || data[0] != byte(MessageTypeHeartbeat) {
		t.Errorf("MarshalBinary() = %v, want [0x01]", data)
	}
}

func TestParseMessageType_ISCP(t *testing.T) {
	data := []byte{0x00, 0x01, 0x02, 0x03}
	msgType, err := ParseMessageType(data)
	if err != nil {
		t.Fatalf("ParseMessageType() error = %v", err)
	}
	if msgType != MessageTypeISCP {
		t.Errorf("ParseMessageType() = %v, want MessageTypeISCP", msgType)
	}
}

func TestParseMessageType_Heartbeat(t *testing.T) {
	data := []byte{0x01}
	msgType, err := ParseMessageType(data)
	if err != nil {
		t.Fatalf("ParseMessageType() error = %v", err)
	}
	if msgType != MessageTypeHeartbeat {
		t.Errorf("ParseMessageType() = %v, want MessageTypeHeartbeat", msgType)
	}
}

func TestParseMessageType_Unknown(t *testing.T) {
	for _, b := range []byte{0x02, 0x0F, 0x10, 0xFF} {
		data := []byte{b}
		_, err := ParseMessageType(data)
		if err == nil {
			t.Errorf("ParseMessageType(0x%02X) expected error, got nil", b)
		}
	}
}

func TestParseMessageType_Empty(t *testing.T) {
	_, err := ParseMessageType([]byte{})
	if err == nil {
		t.Error("ParseMessageType([]) expected error, got nil")
	}
}
