package webtransport_test

import (
	"net/url"
	"testing"

	"github.com/AlekSi/pointer"
	"github.com/aptpod/iscp-go/transport"
	"github.com/aptpod/iscp-go/transport/compress"
	. "github.com/aptpod/iscp-go/transport/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNegotiationParams_Marshal_And_Unmarshal_URLValues(t *testing.T) {
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
				NegotiationParams: transport.NegotiationParams{
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
			values, err := tt.params.MarshalURLValues()
			require.NoError(t, err)
			got := NegotiationParams{}
			require.NoError(t, got.UnmarshalURLValues(values))
			assert.Equal(t, tt.params, got)
		})
	}
}

func TestNegotiationParams_MarshalURLValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		params  NegotiationParams
		want    url.Values
		wantErr bool
	}{
		{
			name:   "default values",
			params: NegotiationParams{},
			want:   url.Values{},
		},
		{
			name: "filled fields",
			params: NegotiationParams{
				NegotiationParams: transport.NegotiationParams{
					Encoding:           transport.EncodingNameProtobuf,
					Compress:           compress.TypeContextTakeOver,
					CompressLevel:      pointer.ToInt(9),
					CompressWindowBits: pointer.ToInt(32),
				},
			},
			want: url.Values{
				"enc":      []string{"proto"},
				"comp":     []string{"context-takeover"},
				"clevel":   []string{"9"},
				"cwinbits": []string{"32"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.params.MarshalURLValues()
			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNegotiationParams_UnmarshalURLValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		values  url.Values
		want    NegotiationParams
		wantErr bool
	}{
		{
			name:   "default values",
			values: url.Values{},
			want:   NegotiationParams{},
		},
		{
			name:   "empty",
			values: url.Values{},
			want:   NegotiationParams{},
		},
		{
			name: "filled fields",
			values: url.Values{
				"enc":      []string{"proto"},
				"comp":     []string{"context-takeover"},
				"clevel":   []string{"9"},
				"cwinbits": []string{"32"},
			},
			want: NegotiationParams{
				NegotiationParams: transport.NegotiationParams{
					Encoding:           transport.EncodingNameProtobuf,
					Compress:           compress.TypeContextTakeOver,
					CompressLevel:      pointer.ToInt(9),
					CompressWindowBits: pointer.ToInt(32),
				},
			},
		},
		{
			name: "even contains unknown key",
			values: url.Values{
				"enc":      []string{"proto"},
				"comp":     []string{"context-takeover"},
				"clevel":   []string{"9"},
				"cwinbits": []string{"32"},
				"hello":    []string{"world"},
			},
			want: NegotiationParams{
				NegotiationParams: transport.NegotiationParams{
					Encoding:           transport.EncodingNameProtobuf,
					Compress:           compress.TypeContextTakeOver,
					CompressLevel:      pointer.ToInt(9),
					CompressWindowBits: pointer.ToInt(32),
				},
			},
		},
		{
			name: "value len is 0",
			values: url.Values{
				"enc": []string{},
			},
			wantErr: true,
		},
		{
			name: "value len is 2",
			values: url.Values{
				"enc": []string{"1", "2"},
			},
			wantErr: true,
		},
		{
			name: "empty key",
			values: url.Values{
				"": []string{"json"},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := NegotiationParams{}
			err := params.UnmarshalURLValues(tt.values)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, params)
		})
	}
}
