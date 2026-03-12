package protocol_test

import (
	"testing"

	"github.com/aptpod/iscp-go/v2/transport/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseMessageType(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		want    protocol.MessageType
		wantErr bool
	}{
		{
			name: "iSCP message",
			data: []byte{0x00, 0x01, 0x02},
			want: protocol.MessageTypeISCP,
		},
		{
			name: "heartbeat message",
			data: []byte{0x01},
			want: protocol.MessageTypeHeartbeat,
		},
		{
			name:    "empty data",
			data:    []byte{},
			wantErr: true,
		},
		{
			name:    "nil data",
			data:    nil,
			wantErr: true,
		},
		{
			name:    "unknown message type",
			data:    []byte{0x02},
			wantErr: true,
		},
		{
			name:    "unknown message type 0xFF",
			data:    []byte{0xFF},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := protocol.ParseMessageType(tt.data)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestHeartbeatMessage_MarshalBinary(t *testing.T) {
	msg := &protocol.HeartbeatMessage{}
	data, err := msg.MarshalBinary()
	require.NoError(t, err)
	assert.Equal(t, []byte{byte(protocol.MessageTypeHeartbeat)}, data)
}

func TestMessageTypeConstants(t *testing.T) {
	assert.Equal(t, protocol.MessageType(0x00), protocol.MessageTypeISCP)
	assert.Equal(t, protocol.MessageType(0x01), protocol.MessageTypeHeartbeat)
}
