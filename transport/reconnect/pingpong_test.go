package reconnect

import (
	"bytes"
	"math"
	"testing"
)

func TestTryParsePing_ValidMessages(t *testing.T) {
	tests := []struct {
		name      string
		data      []byte
		expectSeq uint32
		expectOk  bool
		expectErr bool
	}{
		{
			name:      "valid ping with seq 0",
			data:      []byte{0xFF, 0x00, 0x00, 0x00, 0x00, 0x00},
			expectSeq: 0,
			expectOk:  true,
			expectErr: false,
		},
		{
			name:      "valid ping with seq 1",
			data:      []byte{0xFF, 0x00, 0x00, 0x00, 0x00, 0x01},
			expectSeq: 1,
			expectOk:  true,
			expectErr: false,
		},
		{
			name:      "valid ping with seq 42",
			data:      []byte{0xFF, 0x00, 0x00, 0x00, 0x00, 0x2A},
			expectSeq: 42,
			expectOk:  true,
			expectErr: false,
		},
		{
			name:      "valid ping with max uint32",
			data:      []byte{0xFF, 0x00, 0xFF, 0xFF, 0xFF, 0xFF},
			expectSeq: math.MaxUint32,
			expectOk:  true,
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, ok, err := TryParsePing(tt.data)
			if (err != nil) != tt.expectErr {
				t.Errorf("TryParsePing() error = %v, expectErr %v", err, tt.expectErr)
				return
			}
			if ok != tt.expectOk {
				t.Errorf("TryParsePing() ok = %v, want %v", ok, tt.expectOk)
				return
			}
			if tt.expectOk {
				if msg == nil {
					t.Error("TryParsePing() msg is nil for valid ping message")
					return
				}
				if msg.Sequence != tt.expectSeq {
					t.Errorf("TryParsePing() Sequence = %v, want %v", msg.Sequence, tt.expectSeq)
				}
			}
		})
	}
}

func TestTryParsePong_ValidMessages(t *testing.T) {
	tests := []struct {
		name      string
		data      []byte
		expectSeq uint32
		expectOk  bool
		expectErr bool
	}{
		{
			name:      "valid pong with seq 0",
			data:      []byte{0xFF, 0x01, 0x00, 0x00, 0x00, 0x00},
			expectSeq: 0,
			expectOk:  true,
			expectErr: false,
		},
		{
			name:      "valid pong with seq 1",
			data:      []byte{0xFF, 0x01, 0x00, 0x00, 0x00, 0x01},
			expectSeq: 1,
			expectOk:  true,
			expectErr: false,
		},
		{
			name:      "valid pong with seq 42",
			data:      []byte{0xFF, 0x01, 0x00, 0x00, 0x00, 0x2A},
			expectSeq: 42,
			expectOk:  true,
			expectErr: false,
		},
		{
			name:      "valid pong with max uint32",
			data:      []byte{0xFF, 0x01, 0xFF, 0xFF, 0xFF, 0xFF},
			expectSeq: math.MaxUint32,
			expectOk:  true,
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, ok, err := TryParsePong(tt.data)
			if (err != nil) != tt.expectErr {
				t.Errorf("TryParsePong() error = %v, expectErr %v", err, tt.expectErr)
				return
			}
			if ok != tt.expectOk {
				t.Errorf("TryParsePong() ok = %v, want %v", ok, tt.expectOk)
				return
			}
			if tt.expectOk {
				if msg == nil {
					t.Error("TryParsePong() msg is nil for valid pong message")
					return
				}
				if msg.Sequence != tt.expectSeq {
					t.Errorf("TryParsePong() Sequence = %v, want %v", msg.Sequence, tt.expectSeq)
				}
			}
		})
	}
}

func TestTryParsePing_NonPingMessages(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{
			name: "empty data",
			data: []byte{},
		},
		{
			name: "no magic byte",
			data: []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
		},
		{
			name: "regular message",
			data: []byte("hello world"),
		},
		{
			name: "pong message",
			data: []byte{0xFF, 0x01, 0x00, 0x00, 0x00, 0x00},
		},
		{
			name: "unknown control type",
			data: []byte{0xFF, 0x60, 0x00, 0x00, 0x00, 0x00},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, ok, err := TryParsePing(tt.data)
			if err != nil {
				t.Errorf("TryParsePing() unexpected error = %v", err)
				return
			}
			if ok {
				t.Errorf("TryParsePing() ok = true, want false for non-ping message")
			}
			if msg != nil {
				t.Errorf("TryParsePing() msg = %v, want nil for non-ping message", msg)
			}
		})
	}
}

func TestTryParsePong_NonPongMessages(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{
			name: "empty data",
			data: []byte{},
		},
		{
			name: "no magic byte",
			data: []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
		},
		{
			name: "regular message",
			data: []byte("hello world"),
		},
		{
			name: "ping message",
			data: []byte{0xFF, 0x00, 0x00, 0x00, 0x00, 0x00},
		},
		{
			name: "unknown control type",
			data: []byte{0xFF, 0x60, 0x00, 0x00, 0x00, 0x00},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, ok, err := TryParsePong(tt.data)
			if err != nil {
				t.Errorf("TryParsePong() unexpected error = %v", err)
				return
			}
			if ok {
				t.Errorf("TryParsePong() ok = true, want false for non-pong message")
			}
			if msg != nil {
				t.Errorf("TryParsePong() msg = %v, want nil for non-pong message", msg)
			}
		})
	}
}

func TestTryParsePing_ReservedTypes(t *testing.T) {
	// Test all reserved types (0x02-0x0F)
	for msgType := byte(0x02); msgType <= 0x0F; msgType++ {
		t.Run("reserved type "+string(rune(msgType)), func(t *testing.T) {
			data := []byte{0xFF, msgType, 0x00, 0x00, 0x00, 0x00}
			msg, ok, err := TryParsePing(data)
			if err == nil {
				t.Error("TryParsePing() expected error for reserved type")
			}
			if ok {
				t.Error("TryParsePing() ok = true, want false for reserved type error")
			}
			if msg != nil {
				t.Error("TryParsePing() msg should be nil for reserved type error")
			}
		})
	}
}

func TestTryParsePong_ReservedTypes(t *testing.T) {
	// Test all reserved types (0x02-0x0F)
	for msgType := byte(0x02); msgType <= 0x0F; msgType++ {
		t.Run("reserved type "+string(rune(msgType)), func(t *testing.T) {
			data := []byte{0xFF, msgType, 0x00, 0x00, 0x00, 0x00}
			msg, ok, err := TryParsePong(data)
			if err == nil {
				t.Error("TryParsePong() expected error for reserved type")
			}
			if ok {
				t.Error("TryParsePong() ok = true, want false for reserved type error")
			}
			if msg != nil {
				t.Error("TryParsePong() msg should be nil for reserved type error")
			}
		})
	}
}

func TestTryParsePing_InvalidFormats(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{
			name: "too short - 3 bytes",
			data: []byte{0xFF, 0x00, 0x01},
		},
		{
			name: "too short - 5 bytes",
			data: []byte{0xFF, 0x00, 0x00, 0x00, 0x00},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, ok, err := TryParsePing(tt.data)
			if err == nil {
				t.Error("TryParsePing() expected error for invalid format")
			}
			if ok {
				t.Error("TryParsePing() ok = true, want false for invalid format error")
			}
			if msg != nil {
				t.Error("TryParsePing() msg should be nil for invalid format error")
			}
		})
	}
}

func TestTryParsePong_InvalidFormats(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{
			name: "too short - 3 bytes",
			data: []byte{0xFF, 0x01, 0x01},
		},
		{
			name: "too short - 5 bytes",
			data: []byte{0xFF, 0x01, 0x00, 0x00, 0x00},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, ok, err := TryParsePong(tt.data)
			if err == nil {
				t.Error("TryParsePong() expected error for invalid format")
			}
			if ok {
				t.Error("TryParsePong() ok = true, want false for invalid format error")
			}
			if msg != nil {
				t.Error("TryParsePong() msg should be nil for invalid format error")
			}
		})
	}
}

func TestEncodeDecode_Ping_RoundTrip(t *testing.T) {
	sequences := []uint32{0, 1, 42, 255, 65535, math.MaxUint32}

	for _, seq := range sequences {
		t.Run("ping roundtrip", func(t *testing.T) {
			encoded, _ := (&PingMessage{Sequence: seq}).MarshalBinary()
			msg, ok, err := TryParsePing(encoded)
			if err != nil {
				t.Fatalf("TryParsePing() error = %v", err)
			}
			if !ok {
				t.Fatal("TryParsePing() ok = false, want true")
			}
			if msg.Sequence != seq {
				t.Errorf("Sequence = %v, want %v", msg.Sequence, seq)
			}
		})
	}
}

func TestEncodeDecode_Pong_RoundTrip(t *testing.T) {
	sequences := []uint32{0, 1, 42, 255, 65535, math.MaxUint32}

	for _, seq := range sequences {
		t.Run("pong roundtrip", func(t *testing.T) {
			encoded, _ := (&PongMessage{Sequence: seq}).MarshalBinary()
			msg, ok, err := TryParsePong(encoded)
			if err != nil {
				t.Fatalf("TryParsePong() error = %v", err)
			}
			if !ok {
				t.Fatal("TryParsePong() ok = false, want true")
			}
			if msg.Sequence != seq {
				t.Errorf("Sequence = %v, want %v", msg.Sequence, seq)
			}
		})
	}
}

func TestBigEndianEncoding(t *testing.T) {
	// Test big-endian encoding specifically
	seq := uint32(0x12345678)
	expectedBytes := []byte{0x12, 0x34, 0x56, 0x78}

	pingMsg, _ := (&PingMessage{Sequence: seq}).MarshalBinary()
	if !bytes.Equal(pingMsg[2:6], expectedBytes) {
		t.Errorf("EncodePing() sequence bytes = %v, want %v", pingMsg[2:6], expectedBytes)
	}

	pongMsg, _ := (&PongMessage{Sequence: seq}).MarshalBinary()
	if !bytes.Equal(pongMsg[2:6], expectedBytes) {
		t.Errorf("EncodePong() sequence bytes = %v, want %v", pongMsg[2:6], expectedBytes)
	}
}

func TestPingMessage_MarshalBinary(t *testing.T) {
	tests := []struct {
		name     string
		msg      *PingMessage
		expected []byte
		wantErr  bool
	}{
		{
			name:     "marshal ping with seq 0",
			msg:      &PingMessage{Sequence: 0},
			expected: []byte{0xFF, 0x00, 0x00, 0x00, 0x00, 0x00},
			wantErr:  false,
		},
		{
			name:     "marshal ping with seq 42",
			msg:      &PingMessage{Sequence: 42},
			expected: []byte{0xFF, 0x00, 0x00, 0x00, 0x00, 0x2A},
			wantErr:  false,
		},
		{
			name:     "marshal ping with max uint32",
			msg:      &PingMessage{Sequence: math.MaxUint32},
			expected: []byte{0xFF, 0x00, 0xFF, 0xFF, 0xFF, 0xFF},
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.msg.MarshalBinary()
			if (err != nil) != tt.wantErr {
				t.Errorf("MarshalBinary() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && !bytes.Equal(result, tt.expected) {
				t.Errorf("MarshalBinary() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestPongMessage_MarshalBinary(t *testing.T) {
	tests := []struct {
		name     string
		msg      *PongMessage
		expected []byte
		wantErr  bool
	}{
		{
			name:     "marshal pong with seq 0",
			msg:      &PongMessage{Sequence: 0},
			expected: []byte{0xFF, 0x01, 0x00, 0x00, 0x00, 0x00},
			wantErr:  false,
		},
		{
			name:     "marshal pong with seq 42",
			msg:      &PongMessage{Sequence: 42},
			expected: []byte{0xFF, 0x01, 0x00, 0x00, 0x00, 0x2A},
			wantErr:  false,
		},
		{
			name:     "marshal pong with max uint32",
			msg:      &PongMessage{Sequence: math.MaxUint32},
			expected: []byte{0xFF, 0x01, 0xFF, 0xFF, 0xFF, 0xFF},
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.msg.MarshalBinary()
			if (err != nil) != tt.wantErr {
				t.Errorf("MarshalBinary() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && !bytes.Equal(result, tt.expected) {
				t.Errorf("MarshalBinary() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestPingMessage_UnmarshalBinary(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		wantSeq uint32
		wantErr bool
	}{
		{
			name:    "unmarshal ping with seq 0",
			data:    []byte{0xFF, 0x00, 0x00, 0x00, 0x00, 0x00},
			wantSeq: 0,
			wantErr: false,
		},
		{
			name:    "unmarshal ping with seq 42",
			data:    []byte{0xFF, 0x00, 0x00, 0x00, 0x00, 0x2A},
			wantSeq: 42,
			wantErr: false,
		},
		{
			name:    "unmarshal ping with max uint32",
			data:    []byte{0xFF, 0x00, 0xFF, 0xFF, 0xFF, 0xFF},
			wantSeq: math.MaxUint32,
			wantErr: false,
		},
		{
			name:    "unmarshal non-ping message",
			data:    []byte("hello world"),
			wantSeq: 0,
			wantErr: true,
		},
		{
			name:    "unmarshal pong message",
			data:    []byte{0xFF, 0x01, 0x00, 0x00, 0x00, 0x00},
			wantSeq: 0,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := &PingMessage{}
			err := msg.UnmarshalBinary(tt.data)
			if (err != nil) != tt.wantErr {
				t.Errorf("UnmarshalBinary() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if msg.Sequence != tt.wantSeq {
					t.Errorf("Sequence = %v, want %v", msg.Sequence, tt.wantSeq)
				}
			}
		})
	}
}

func TestPongMessage_UnmarshalBinary(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		wantSeq uint32
		wantErr bool
	}{
		{
			name:    "unmarshal pong with seq 0",
			data:    []byte{0xFF, 0x01, 0x00, 0x00, 0x00, 0x00},
			wantSeq: 0,
			wantErr: false,
		},
		{
			name:    "unmarshal pong with seq 42",
			data:    []byte{0xFF, 0x01, 0x00, 0x00, 0x00, 0x2A},
			wantSeq: 42,
			wantErr: false,
		},
		{
			name:    "unmarshal pong with max uint32",
			data:    []byte{0xFF, 0x01, 0xFF, 0xFF, 0xFF, 0xFF},
			wantSeq: math.MaxUint32,
			wantErr: false,
		},
		{
			name:    "unmarshal non-pong message",
			data:    []byte("hello world"),
			wantSeq: 0,
			wantErr: true,
		},
		{
			name:    "unmarshal ping message",
			data:    []byte{0xFF, 0x00, 0x00, 0x00, 0x00, 0x00},
			wantSeq: 0,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := &PongMessage{}
			err := msg.UnmarshalBinary(tt.data)
			if (err != nil) != tt.wantErr {
				t.Errorf("UnmarshalBinary() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if msg.Sequence != tt.wantSeq {
					t.Errorf("Sequence = %v, want %v", msg.Sequence, tt.wantSeq)
				}
			}
		})
	}
}

func TestPingMessage_MarshalUnmarshal_RoundTrip(t *testing.T) {
	tests := []struct {
		name string
		msg  *PingMessage
	}{
		{
			name: "ping with seq 0",
			msg:  &PingMessage{Sequence: 0},
		},
		{
			name: "ping with seq 42",
			msg:  &PingMessage{Sequence: 42},
		},
		{
			name: "ping with max uint32",
			msg:  &PingMessage{Sequence: math.MaxUint32},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Marshal
			data, err := tt.msg.MarshalBinary()
			if err != nil {
				t.Fatalf("MarshalBinary() error = %v", err)
			}

			// Unmarshal
			result := &PingMessage{}
			if err := result.UnmarshalBinary(data); err != nil {
				t.Fatalf("UnmarshalBinary() error = %v", err)
			}

			// Compare
			if result.Sequence != tt.msg.Sequence {
				t.Errorf("Sequence = %v, want %v", result.Sequence, tt.msg.Sequence)
			}
		})
	}
}

func TestPongMessage_MarshalUnmarshal_RoundTrip(t *testing.T) {
	tests := []struct {
		name string
		msg  *PongMessage
	}{
		{
			name: "pong with seq 0",
			msg:  &PongMessage{Sequence: 0},
		},
		{
			name: "pong with seq 42",
			msg:  &PongMessage{Sequence: 42},
		},
		{
			name: "pong with max uint32",
			msg:  &PongMessage{Sequence: math.MaxUint32},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Marshal
			data, err := tt.msg.MarshalBinary()
			if err != nil {
				t.Fatalf("MarshalBinary() error = %v", err)
			}

			// Unmarshal
			result := &PongMessage{}
			if err := result.UnmarshalBinary(data); err != nil {
				t.Fatalf("UnmarshalBinary() error = %v", err)
			}

			// Compare
			if result.Sequence != tt.msg.Sequence {
				t.Errorf("Sequence = %v, want %v", result.Sequence, tt.msg.Sequence)
			}
		})
	}
}

// TestTryParseControlMessage_ValidMessages tests parsing valid ping and pong control messages
func TestTryParseControlMessage_ValidMessages(t *testing.T) {
	tests := []struct {
		name         string
		data         []byte
		expectSeq    uint32
		expectType   string // "ping" or "pong"
		expectOk     bool
		expectErr    bool
	}{
		{
			name:       "valid ping with seq 0",
			data:       []byte{0xFF, 0x00, 0x00, 0x00, 0x00, 0x00},
			expectSeq:  0,
			expectType: "ping",
			expectOk:   true,
			expectErr:  false,
		},
		{
			name:       "valid ping with seq 42",
			data:       []byte{0xFF, 0x00, 0x00, 0x00, 0x00, 0x2A},
			expectSeq:  42,
			expectType: "ping",
			expectOk:   true,
			expectErr:  false,
		},
		{
			name:       "valid ping with max uint32",
			data:       []byte{0xFF, 0x00, 0xFF, 0xFF, 0xFF, 0xFF},
			expectSeq:  math.MaxUint32,
			expectType: "ping",
			expectOk:   true,
			expectErr:  false,
		},
		{
			name:       "valid pong with seq 0",
			data:       []byte{0xFF, 0x01, 0x00, 0x00, 0x00, 0x00},
			expectSeq:  0,
			expectType: "pong",
			expectOk:   true,
			expectErr:  false,
		},
		{
			name:       "valid pong with seq 100",
			data:       []byte{0xFF, 0x01, 0x00, 0x00, 0x00, 0x64},
			expectSeq:  100,
			expectType: "pong",
			expectOk:   true,
			expectErr:  false,
		},
		{
			name:       "valid pong with max uint32",
			data:       []byte{0xFF, 0x01, 0xFF, 0xFF, 0xFF, 0xFF},
			expectSeq:  math.MaxUint32,
			expectType: "pong",
			expectOk:   true,
			expectErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, ok, err := TryParseControlMessage(tt.data)
			if (err != nil) != tt.expectErr {
				t.Errorf("TryParseControlMessage() error = %v, expectErr %v", err, tt.expectErr)
				return
			}
			if ok != tt.expectOk {
				t.Errorf("TryParseControlMessage() ok = %v, want %v", ok, tt.expectOk)
				return
			}
			if tt.expectOk {
				if msg == nil {
					t.Error("TryParseControlMessage() msg is nil for valid control message")
					return
				}
				if msg.GetSequence() != tt.expectSeq {
					t.Errorf("TryParseControlMessage() Sequence = %v, want %v", msg.GetSequence(), tt.expectSeq)
				}

				// Type assertion based on expected type
				switch tt.expectType {
				case "ping":
					if _, ok := msg.(*PingMessage); !ok {
						t.Errorf("TryParseControlMessage() message type is not *PingMessage")
					}
				case "pong":
					if _, ok := msg.(*PongMessage); !ok {
						t.Errorf("TryParseControlMessage() message type is not *PongMessage")
					}
				}
			}
		})
	}
}

// TestTryParseControlMessage_NonControlMessages tests parsing non-control messages
func TestTryParseControlMessage_NonControlMessages(t *testing.T) {
	tests := []struct {
		name      string
		data      []byte
		expectOk  bool
		expectErr bool
	}{
		{
			name:      "empty data",
			data:      []byte{},
			expectOk:  false,
			expectErr: false,
		},
		{
			name:      "non-control message (0x00 magic)",
			data:      []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05},
			expectOk:  false,
			expectErr: false,
		},
		{
			name:      "non-control message (0xAA magic)",
			data:      []byte{0xAA, 0x00, 0x00, 0x00, 0x00, 0x00},
			expectOk:  false,
			expectErr: false,
		},
		{
			name:      "regular data",
			data:      []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08},
			expectOk:  false,
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, ok, err := TryParseControlMessage(tt.data)
			if (err != nil) != tt.expectErr {
				t.Errorf("TryParseControlMessage() error = %v, expectErr %v", err, tt.expectErr)
				return
			}
			if ok != tt.expectOk {
				t.Errorf("TryParseControlMessage() ok = %v, want %v", ok, tt.expectOk)
				return
			}
			if !tt.expectOk && msg != nil {
				t.Error("TryParseControlMessage() msg should be nil for non-control messages")
			}
		})
	}
}

// TestTryParseControlMessage_ProtocolErrors tests protocol error handling
func TestTryParseControlMessage_ProtocolErrors(t *testing.T) {
	tests := []struct {
		name      string
		data      []byte
		expectOk  bool
		expectErr bool
	}{
		{
			name:      "too short (1 byte)",
			data:      []byte{0xFF},
			expectOk:  false,
			expectErr: true,
		},
		{
			name:      "reserved message type 0x02",
			data:      []byte{0xFF, 0x02, 0x00, 0x00, 0x00, 0x00},
			expectOk:  false,
			expectErr: true,
		},
		{
			name:      "reserved message type 0x0F",
			data:      []byte{0xFF, 0x0F, 0x00, 0x00, 0x00, 0x00},
			expectOk:  false,
			expectErr: true,
		},
		{
			name:      "message too short (5 bytes)",
			data:      []byte{0xFF, 0x00, 0x00, 0x00, 0x00},
			expectOk:  false,
			expectErr: true,
		},
		{
			name:      "message too short (3 bytes)",
			data:      []byte{0xFF, 0x01, 0x00},
			expectOk:  false,
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, ok, err := TryParseControlMessage(tt.data)
			if (err != nil) != tt.expectErr {
				t.Errorf("TryParseControlMessage() error = %v, expectErr %v", err, tt.expectErr)
				return
			}
			if ok != tt.expectOk {
				t.Errorf("TryParseControlMessage() ok = %v, want %v", ok, tt.expectOk)
				return
			}
			if tt.expectErr && msg != nil {
				t.Error("TryParseControlMessage() msg should be nil on protocol error")
			}
		})
	}
}

// TestTryParseControlMessage_UnknownMessageTypes tests handling of unknown message types
func TestTryParseControlMessage_UnknownMessageTypes(t *testing.T) {
	tests := []struct {
		name      string
		data      []byte
		expectOk  bool
		expectErr bool
	}{
		{
			name:      "unknown type 0x10 (after reserved range)",
			data:      []byte{0xFF, 0x10, 0x00, 0x00, 0x00, 0x00},
			expectOk:  false,
			expectErr: false,
		},
		{
			name:      "unknown type 0xFF",
			data:      []byte{0xFF, 0xFF, 0x00, 0x00, 0x00, 0x00},
			expectOk:  false,
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, ok, err := TryParseControlMessage(tt.data)
			if (err != nil) != tt.expectErr {
				t.Errorf("TryParseControlMessage() error = %v, expectErr %v", err, tt.expectErr)
				return
			}
			if ok != tt.expectOk {
				t.Errorf("TryParseControlMessage() ok = %v, want %v", ok, tt.expectOk)
				return
			}
			if msg != nil {
				t.Error("TryParseControlMessage() msg should be nil for unknown message types")
			}
		})
	}
}

// TestControlMessageInterface_PingMessage tests that PingMessage implements ControlMessage
func TestControlMessageInterface_PingMessage(t *testing.T) {
	ping := &PingMessage{Sequence: 42}
	
	var msg ControlMessage = ping
	if msg.GetSequence() != 42 {
		t.Errorf("ControlMessage.GetSequence() = %v, want 42", msg.GetSequence())
	}
}

// TestControlMessageInterface_PongMessage tests that PongMessage implements ControlMessage
func TestControlMessageInterface_PongMessage(t *testing.T) {
	pong := &PongMessage{Sequence: 100}
	
	var msg ControlMessage = pong
	if msg.GetSequence() != 100 {
		t.Errorf("ControlMessage.GetSequence() = %v, want 100", msg.GetSequence())
	}
}
