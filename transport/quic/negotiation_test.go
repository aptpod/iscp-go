package quic_test

import (
	"encoding/binary"
	"testing"

	"github.com/AlekSi/pointer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aptpod/iscp-go/transport"
	"github.com/aptpod/iscp-go/transport/compress"
	. "github.com/aptpod/iscp-go/transport/quic"
)

func TestNegotiationParams_Unmarshal(t *testing.T) {
	t.Parallel()

	marshal := func(vals ...string) []byte {
		bs := []byte{}
		lenbuf := make([]byte, 2)
		for _, v := range vals {
			binary.BigEndian.PutUint16(lenbuf, uint16(len(v)))
			bs = append(bs, lenbuf...)
			bs = append(bs, []byte(v)...)
		}
		return bs
	}

	tests := []struct {
		name    string
		bytes   []byte
		want    NegotiationParams
		wantErr bool
	}{
		{
			name: "filled fields",
			bytes: marshal(
				"enc", "proto",
				"comp", "context-takeover",
				"clevel", "9",
				"cwinbits", "16",
			),
			want: NegotiationParams{
				transport.NegotiationParams{
					Encoding:           transport.EncodingNameProtobuf,
					Compress:           compress.TypeContextTakeOver,
					CompressLevel:      pointer.ToInt(9),
					CompressWindowBits: pointer.ToInt(16),
				},
			},
		},
		{
			name:  "unknown keys",
			bytes: marshal("unknown", "value"),
			want:  NegotiationParams{},
		},
		{
			name:    "duplicated key",
			bytes:   marshal("enc", "json", "enc", "protobuf"),
			wantErr: true,
		},
		{
			name:    "missing value",
			bytes:   marshal("enc"),
			wantErr: true,
		},
		{
			name:    "empty key",
			bytes:   marshal("", "json"),
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NegotiationParams{}
			err := got.Unmarshal(tt.bytes)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNegotiationParams_Marshal_And_Unmarshal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		params NegotiationParams
	}{
		{
			name:   "default values",
			params: NegotiationParams{},
		},
		{
			name: "filled fields",
			params: NegotiationParams{
				transport.NegotiationParams{
					Encoding:           transport.EncodingNameProtobuf,
					Compress:           compress.TypeContextTakeOver,
					CompressLevel:      pointer.ToInt(9),
					CompressWindowBits: pointer.ToInt(32),
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := tt.params.Marshal()
			require.NoError(t, err)

			got := NegotiationParams{}
			require.NoError(t, got.Unmarshal(b))
			assert.Equal(t, tt.params, got)
		})
	}
}
