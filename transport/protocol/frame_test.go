package protocol_test

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/aptpod/iscp-go/transport/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodeLengthPrefix(t *testing.T) {
	tests := []struct {
		name   string
		length uint32
		want   []byte
	}{
		{
			name:   "zero length",
			length: 0,
			want:   []byte{0x00, 0x00, 0x00, 0x00},
		},
		{
			name:   "small length",
			length: 256,
			want:   []byte{0x00, 0x00, 0x01, 0x00},
		},
		{
			name:   "large length",
			length: 0x01020304,
			want:   []byte{0x01, 0x02, 0x03, 0x04},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := protocol.EncodeLengthPrefix(tt.length)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDecodeLengthPrefix(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		want    uint32
		wantErr bool
	}{
		{
			name: "valid prefix",
			data: []byte{0x00, 0x00, 0x01, 0x00},
			want: 256,
		},
		{
			name: "extra bytes ignored",
			data: []byte{0x00, 0x00, 0x01, 0x00, 0xFF, 0xFF},
			want: 256,
		},
		{
			name:    "too short",
			data:    []byte{0x00, 0x00, 0x01},
			wantErr: true,
		},
		{
			name:    "empty",
			data:    []byte{},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := protocol.DecodeLengthPrefix(tt.data)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFrameMessage(t *testing.T) {
	msg := []byte("hello")
	framed := protocol.FrameMessage(msg)

	// Check length prefix
	length := binary.BigEndian.Uint32(framed[:protocol.LengthPrefixSize])
	assert.Equal(t, uint32(len(msg)), length)

	// Check payload
	assert.Equal(t, msg, framed[protocol.LengthPrefixSize:])
}

func TestFrameMessage_Empty(t *testing.T) {
	framed := protocol.FrameMessage([]byte{})

	length := binary.BigEndian.Uint32(framed[:protocol.LengthPrefixSize])
	assert.Equal(t, uint32(0), length)
	assert.Equal(t, protocol.LengthPrefixSize, len(framed))
}

func TestSplitIntoChunks(t *testing.T) {
	tests := []struct {
		name         string
		data         []byte
		maxChunkSize int
		wantChunks   int
		wantSizes    []int
	}{
		{
			name:         "exact fit",
			data:         make([]byte, 16),
			maxChunkSize: 8,
			wantChunks:   2,
			wantSizes:    []int{8, 8},
		},
		{
			name:         "with remainder",
			data:         make([]byte, 10),
			maxChunkSize: 8,
			wantChunks:   2,
			wantSizes:    []int{8, 2},
		},
		{
			name:         "single chunk",
			data:         make([]byte, 5),
			maxChunkSize: 8,
			wantChunks:   1,
			wantSizes:    []int{5},
		},
		{
			name:         "empty data",
			data:         []byte{},
			maxChunkSize: 8,
			wantChunks:   0,
		},
		{
			name:         "zero chunk size uses default",
			data:         make([]byte, protocol.DefaultMaxChunkSize+1),
			maxChunkSize: 0,
			wantChunks:   2,
			wantSizes:    []int{protocol.DefaultMaxChunkSize, 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunks := protocol.SplitIntoChunks(tt.data, tt.maxChunkSize)
			assert.Len(t, chunks, tt.wantChunks)
			for i, wantSize := range tt.wantSizes {
				assert.Len(t, chunks[i], wantSize)
			}
		})
	}
}

func TestWriteTo(t *testing.T) {
	payload := []byte("hello world")
	var buf bytes.Buffer

	n, err := protocol.WriteTo(&buf, payload)
	require.NoError(t, err)
	assert.Equal(t, protocol.LengthPrefixSize+len(payload), n)

	// Verify the written data
	written := buf.Bytes()
	length := binary.BigEndian.Uint32(written[:protocol.LengthPrefixSize])
	assert.Equal(t, uint32(len(payload)), length)
	assert.Equal(t, payload, written[protocol.LengthPrefixSize:])
}

func TestReadFrom(t *testing.T) {
	payload := []byte("hello world")
	var buf bytes.Buffer
	prefix := protocol.EncodeLengthPrefix(uint32(len(payload)))
	buf.Write(prefix)
	buf.Write(payload)

	got, err := protocol.ReadFrom(&buf)
	require.NoError(t, err)
	assert.Equal(t, payload, got)
}

func TestWriteToReadFrom_Roundtrip(t *testing.T) {
	payload := []byte("roundtrip test data")
	var buf bytes.Buffer

	_, err := protocol.WriteTo(&buf, payload)
	require.NoError(t, err)

	got, err := protocol.ReadFrom(&buf)
	require.NoError(t, err)
	assert.Equal(t, payload, got)
}

func TestReadFrom_EmptyReader(t *testing.T) {
	var buf bytes.Buffer
	_, err := protocol.ReadFrom(&buf)
	require.Error(t, err)
}

func TestConstants(t *testing.T) {
	assert.Equal(t, 4, protocol.LengthPrefixSize)
	assert.Equal(t, 8*1024, protocol.DefaultMaxChunkSize)
}
