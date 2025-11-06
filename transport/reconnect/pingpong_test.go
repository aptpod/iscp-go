package reconnect

import (
	"bytes"
	"math"
	"testing"
)

func TestEncodePing(t *testing.T) {
	tests := []struct {
		name     string
		seq      uint32
		expected []byte
	}{
		{
			name:     "sequence 0",
			seq:      0,
			expected: []byte{0xFF, 0x00, 0x00, 0x00, 0x00, 0x00},
		},
		{
			name:     "sequence 1",
			seq:      1,
			expected: []byte{0xFF, 0x00, 0x00, 0x00, 0x00, 0x01},
		},
		{
			name:     "sequence 42",
			seq:      42,
			expected: []byte{0xFF, 0x00, 0x00, 0x00, 0x00, 0x2A},
		},
		{
			name:     "sequence 255",
			seq:      255,
			expected: []byte{0xFF, 0x00, 0x00, 0x00, 0x00, 0xFF},
		},
		{
			name:     "sequence 65535",
			seq:      65535,
			expected: []byte{0xFF, 0x00, 0x00, 0x00, 0xFF, 0xFF},
		},
		{
			name:     "sequence max uint32",
			seq:      math.MaxUint32,
			expected: []byte{0xFF, 0x00, 0xFF, 0xFF, 0xFF, 0xFF},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := EncodePing(tt.seq)
			if !bytes.Equal(result, tt.expected) {
				t.Errorf("EncodePing(%d) = %v, want %v", tt.seq, result, tt.expected)
			}
		})
	}
}

func TestEncodePong(t *testing.T) {
	tests := []struct {
		name     string
		seq      uint32
		expected []byte
	}{
		{
			name:     "sequence 0",
			seq:      0,
			expected: []byte{0xFF, 0x01, 0x00, 0x00, 0x00, 0x00},
		},
		{
			name:     "sequence 1",
			seq:      1,
			expected: []byte{0xFF, 0x01, 0x00, 0x00, 0x00, 0x01},
		},
		{
			name:     "sequence 42",
			seq:      42,
			expected: []byte{0xFF, 0x01, 0x00, 0x00, 0x00, 0x2A},
		},
		{
			name:     "sequence 255",
			seq:      255,
			expected: []byte{0xFF, 0x01, 0x00, 0x00, 0x00, 0xFF},
		},
		{
			name:     "sequence 65535",
			seq:      65535,
			expected: []byte{0xFF, 0x01, 0x00, 0x00, 0xFF, 0xFF},
		},
		{
			name:     "sequence max uint32",
			seq:      math.MaxUint32,
			expected: []byte{0xFF, 0x01, 0xFF, 0xFF, 0xFF, 0xFF},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := EncodePong(tt.seq)
			if !bytes.Equal(result, tt.expected) {
				t.Errorf("EncodePong(%d) = %v, want %v", tt.seq, result, tt.expected)
			}
		})
	}
}

func TestDecodePingPong_ValidMessages(t *testing.T) {
	tests := []struct {
		name         string
		data         []byte
		expectType   MessageType
		expectSeq    uint32
		expectIsCtrl bool
		expectErr    bool
	}{
		{
			name:         "valid ping with seq 0",
			data:         []byte{0xFF, 0x00, 0x00, 0x00, 0x00, 0x00},
			expectType:   MessageTypePing,
			expectSeq:    0,
			expectIsCtrl: true,
			expectErr:    false,
		},
		{
			name:         "valid ping with seq 1",
			data:         []byte{0xFF, 0x00, 0x00, 0x00, 0x00, 0x01},
			expectType:   MessageTypePing,
			expectSeq:    1,
			expectIsCtrl: true,
			expectErr:    false,
		},
		{
			name:         "valid ping with seq 42",
			data:         []byte{0xFF, 0x00, 0x00, 0x00, 0x00, 0x2A},
			expectType:   MessageTypePing,
			expectSeq:    42,
			expectIsCtrl: true,
			expectErr:    false,
		},
		{
			name:         "valid pong with seq 0",
			data:         []byte{0xFF, 0x01, 0x00, 0x00, 0x00, 0x00},
			expectType:   MessageTypePong,
			expectSeq:    0,
			expectIsCtrl: true,
			expectErr:    false,
		},
		{
			name:         "valid pong with seq 1",
			data:         []byte{0xFF, 0x01, 0x00, 0x00, 0x00, 0x01},
			expectType:   MessageTypePong,
			expectSeq:    1,
			expectIsCtrl: true,
			expectErr:    false,
		},
		{
			name:         "valid pong with max uint32",
			data:         []byte{0xFF, 0x01, 0xFF, 0xFF, 0xFF, 0xFF},
			expectType:   MessageTypePong,
			expectSeq:    math.MaxUint32,
			expectIsCtrl: true,
			expectErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, isCtrl, err := DecodePingPong(tt.data)
			if (err != nil) != tt.expectErr {
				t.Errorf("DecodePingPong() error = %v, expectErr %v", err, tt.expectErr)
				return
			}
			if isCtrl != tt.expectIsCtrl {
				t.Errorf("DecodePingPong() isCtrl = %v, want %v", isCtrl, tt.expectIsCtrl)
				return
			}
			if tt.expectIsCtrl {
				if msg == nil {
					t.Error("DecodePingPong() msg is nil for control message")
					return
				}
				if msg.Type != tt.expectType {
					t.Errorf("DecodePingPong() Type = %v, want %v", msg.Type, tt.expectType)
				}
				if msg.Sequence != tt.expectSeq {
					t.Errorf("DecodePingPong() Sequence = %v, want %v", msg.Sequence, tt.expectSeq)
				}
			}
		})
	}
}

func TestDecodePingPong_NonControlMessages(t *testing.T) {
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
			name: "unknown control type",
			data: []byte{0xFF, 0x10, 0x00, 0x00, 0x00, 0x00},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, isCtrl, err := DecodePingPong(tt.data)
			if err != nil {
				t.Errorf("DecodePingPong() unexpected error = %v", err)
				return
			}
			if isCtrl {
				t.Errorf("DecodePingPong() isCtrl = true, want false for non-control message")
			}
			if msg != nil {
				t.Errorf("DecodePingPong() msg = %v, want nil for non-control message", msg)
			}
		})
	}
}

func TestDecodePingPong_ReservedTypes(t *testing.T) {
	// Test all reserved types (0x02-0x0F)
	for msgType := byte(0x02); msgType <= 0x0F; msgType++ {
		t.Run("reserved type "+string(rune(msgType)), func(t *testing.T) {
			data := []byte{0xFF, msgType, 0x00, 0x00, 0x00, 0x00}
			msg, isCtrl, err := DecodePingPong(data)
			if err == nil {
				t.Error("DecodePingPong() expected error for reserved type")
			}
			if isCtrl {
				t.Error("DecodePingPong() isCtrl = true, want false for reserved type error")
			}
			if msg != nil {
				t.Error("DecodePingPong() msg should be nil for reserved type error")
			}
		})
	}
}

func TestDecodePingPong_InvalidFormats(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{
			name: "too short - 1 byte",
			data: []byte{0xFF},
		},
		{
			name: "too short - 5 bytes",
			data: []byte{0xFF, 0x00, 0x00, 0x00, 0x00},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, isCtrl, err := DecodePingPong(tt.data)
			if err == nil {
				t.Error("DecodePingPong() expected error for invalid format")
			}
			if isCtrl {
				t.Error("DecodePingPong() isCtrl = true, want false for invalid format error")
			}
			if msg != nil {
				t.Error("DecodePingPong() msg should be nil for invalid format error")
			}
		})
	}
}

func TestEncodeDecode_RoundTrip(t *testing.T) {
	sequences := []uint32{0, 1, 42, 255, 65535, math.MaxUint32}

	for _, seq := range sequences {
		t.Run("ping roundtrip", func(t *testing.T) {
			encoded := EncodePing(seq)
			msg, isCtrl, err := DecodePingPong(encoded)
			if err != nil {
				t.Fatalf("DecodePingPong() error = %v", err)
			}
			if !isCtrl {
				t.Fatal("DecodePingPong() isCtrl = false, want true")
			}
			if msg.Type != MessageTypePing {
				t.Errorf("Type = %v, want %v", msg.Type, MessageTypePing)
			}
			if msg.Sequence != seq {
				t.Errorf("Sequence = %v, want %v", msg.Sequence, seq)
			}
		})

		t.Run("pong roundtrip", func(t *testing.T) {
			encoded := EncodePong(seq)
			msg, isCtrl, err := DecodePingPong(encoded)
			if err != nil {
				t.Fatalf("DecodePingPong() error = %v", err)
			}
			if !isCtrl {
				t.Fatal("DecodePingPong() isCtrl = false, want true")
			}
			if msg.Type != MessageTypePong {
				t.Errorf("Type = %v, want %v", msg.Type, MessageTypePong)
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

	pingMsg := EncodePing(seq)
	if !bytes.Equal(pingMsg[2:6], expectedBytes) {
		t.Errorf("EncodePing() sequence bytes = %v, want %v", pingMsg[2:6], expectedBytes)
	}

	pongMsg := EncodePong(seq)
	if !bytes.Equal(pongMsg[2:6], expectedBytes) {
		t.Errorf("EncodePong() sequence bytes = %v, want %v", pongMsg[2:6], expectedBytes)
	}
}

func TestParsePingPongMessage(t *testing.T) {
	tests := []struct {
		name     string
		msgType  MessageType
		seq      uint32
		wantType MessageType
		wantSeq  uint32
	}{
		{
			name:     "parse ping with seq 0",
			msgType:  MessageTypePing,
			seq:      0,
			wantType: MessageTypePing,
			wantSeq:  0,
		},
		{
			name:     "parse ping with seq 42",
			msgType:  MessageTypePing,
			seq:      42,
			wantType: MessageTypePing,
			wantSeq:  42,
		},
		{
			name:     "parse pong with seq 0",
			msgType:  MessageTypePong,
			seq:      0,
			wantType: MessageTypePong,
			wantSeq:  0,
		},
		{
			name:     "parse pong with max uint32",
			msgType:  MessageTypePong,
			seq:      math.MaxUint32,
			wantType: MessageTypePong,
			wantSeq:  math.MaxUint32,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := ParsePingPongMessage(tt.msgType, tt.seq)
			if msg == nil {
				t.Fatal("ParsePingPongMessage() returned nil")
			}
			if msg.Type != tt.wantType {
				t.Errorf("Type = %v, want %v", msg.Type, tt.wantType)
			}
			if msg.Sequence != tt.wantSeq {
				t.Errorf("Sequence = %v, want %v", msg.Sequence, tt.wantSeq)
			}
		})
	}
}

func TestPingPongMessage_MarshalBinary(t *testing.T) {
	tests := []struct {
		name     string
		msg      *PingPongMessage
		expected []byte
		wantErr  bool
	}{
		{
			name:     "marshal ping with seq 0",
			msg:      &PingPongMessage{Type: MessageTypePing, Sequence: 0},
			expected: []byte{0xFF, 0x00, 0x00, 0x00, 0x00, 0x00},
			wantErr:  false,
		},
		{
			name:     "marshal ping with seq 42",
			msg:      &PingPongMessage{Type: MessageTypePing, Sequence: 42},
			expected: []byte{0xFF, 0x00, 0x00, 0x00, 0x00, 0x2A},
			wantErr:  false,
		},
		{
			name:     "marshal pong with seq 0",
			msg:      &PingPongMessage{Type: MessageTypePong, Sequence: 0},
			expected: []byte{0xFF, 0x01, 0x00, 0x00, 0x00, 0x00},
			wantErr:  false,
		},
		{
			name:     "marshal pong with max uint32",
			msg:      &PingPongMessage{Type: MessageTypePong, Sequence: math.MaxUint32},
			expected: []byte{0xFF, 0x01, 0xFF, 0xFF, 0xFF, 0xFF},
			wantErr:  false,
		},
		{
			name:     "marshal invalid type",
			msg:      &PingPongMessage{Type: 0x60, Sequence: 0},
			expected: nil,
			wantErr:  true,
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

func TestPingPongMessage_UnmarshalBinary(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		wantType MessageType
		wantSeq  uint32
		wantErr  bool
	}{
		{
			name:     "unmarshal ping with seq 0",
			data:     []byte{0xFF, 0x00, 0x00, 0x00, 0x00, 0x00},
			wantType: MessageTypePing,
			wantSeq:  0,
			wantErr:  false,
		},
		{
			name:     "unmarshal ping with seq 42",
			data:     []byte{0xFF, 0x00, 0x00, 0x00, 0x00, 0x2A},
			wantType: MessageTypePing,
			wantSeq:  42,
			wantErr:  false,
		},
		{
			name:     "unmarshal pong with seq 0",
			data:     []byte{0xFF, 0x01, 0x00, 0x00, 0x00, 0x00},
			wantType: MessageTypePong,
			wantSeq:  0,
			wantErr:  false,
		},
		{
			name:     "unmarshal pong with max uint32",
			data:     []byte{0xFF, 0x01, 0xFF, 0xFF, 0xFF, 0xFF},
			wantType: MessageTypePong,
			wantSeq:  math.MaxUint32,
			wantErr:  false,
		},
		{
			name:     "unmarshal non-control message",
			data:     []byte("hello world"),
			wantType: 0,
			wantSeq:  0,
			wantErr:  true,
		},
		{
			name:     "unmarshal invalid message",
			data:     []byte{0xFF, 0x02, 0x00, 0x00, 0x00, 0x00},
			wantType: 0,
			wantSeq:  0,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := &PingPongMessage{}
			err := msg.UnmarshalBinary(tt.data)
			if (err != nil) != tt.wantErr {
				t.Errorf("UnmarshalBinary() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if msg.Type != tt.wantType {
					t.Errorf("Type = %v, want %v", msg.Type, tt.wantType)
				}
				if msg.Sequence != tt.wantSeq {
					t.Errorf("Sequence = %v, want %v", msg.Sequence, tt.wantSeq)
				}
			}
		})
	}
}

func TestPingPongMessage_MarshalUnmarshal_RoundTrip(t *testing.T) {
	tests := []struct {
		name string
		msg  *PingPongMessage
	}{
		{
			name: "ping with seq 0",
			msg:  &PingPongMessage{Type: MessageTypePing, Sequence: 0},
		},
		{
			name: "ping with seq 42",
			msg:  &PingPongMessage{Type: MessageTypePing, Sequence: 42},
		},
		{
			name: "pong with seq 0",
			msg:  &PingPongMessage{Type: MessageTypePong, Sequence: 0},
		},
		{
			name: "pong with max uint32",
			msg:  &PingPongMessage{Type: MessageTypePong, Sequence: math.MaxUint32},
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
			result := &PingPongMessage{}
			if err := result.UnmarshalBinary(data); err != nil {
				t.Fatalf("UnmarshalBinary() error = %v", err)
			}

			// Compare
			if result.Type != tt.msg.Type {
				t.Errorf("Type = %v, want %v", result.Type, tt.msg.Type)
			}
			if result.Sequence != tt.msg.Sequence {
				t.Errorf("Sequence = %v, want %v", result.Sequence, tt.msg.Sequence)
			}
		})
	}
}
