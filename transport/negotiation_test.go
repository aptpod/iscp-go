package transport_test

import (
	"testing"

	"github.com/AlekSi/pointer"
	. "github.com/aptpod/iscp-go/transport"
	"github.com/aptpod/iscp-go/transport/compress"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNegotiationParams_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		params  NegotiationParams
		wantErr bool
	}{
		{
			name:   "default values",
			params: NegotiationParams{},
		},
		{
			name: "filled fields",
			params: NegotiationParams{
				Encoding:           EncodingNameProtobuf,
				Compress:           compress.TypeContextTakeOver,
				CompressLevel:      pointer.ToInt(9),
				CompressWindowBits: pointer.ToInt(16),
				Reconnect:          true,
				TransportID:        "test",
			},
		},
		{
			name: "invalid encoding",
			params: NegotiationParams{
				Encoding: EncodingName("unknown"),
			},
			wantErr: true,
		},
		{
			name: "invalid compress",
			params: NegotiationParams{
				Compress: compress.Type("unknown"),
			},
			wantErr: true,
		},
		{
			name: "invalid compress level",
			params: NegotiationParams{
				Compress:      compress.TypePerMessage,
				CompressLevel: pointer.ToInt(10),
			},
			wantErr: true,
		},
		{
			name: "invalid compress windows bits",
			params: NegotiationParams{
				Compress:           compress.TypePerMessage,
				CompressWindowBits: pointer.ToInt(33),
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.params.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestNegotiationParams_Validate_Apply_DefaultCompression_Level(t *testing.T) {
	t.Parallel()

	p := NegotiationParams{
		Compress: compress.TypePerMessage,
	}
	assert.Nil(t, p.CompressLevel)
	assert.NoError(t, p.Validate())
	assert.Equal(t, 6, *p.CompressLevel)
}

func TestNegotiationParams_Marshal_And_Unmarshal_KeyValues(t *testing.T) {
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
				Encoding:           EncodingNameProtobuf,
				Compress:           compress.TypeContextTakeOver,
				CompressLevel:      pointer.ToInt(9),
				CompressWindowBits: pointer.ToInt(16),
			},
		},
		{
			name: "even invalid values",
			params: NegotiationParams{
				Encoding:           EncodingName("unknown"),
				Compress:           compress.Type("unknown"),
				CompressLevel:      pointer.ToInt(100),
				CompressWindowBits: pointer.ToInt(200),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			marshaled, err := tt.params.MarshalKeyValues()
			require.NoError(t, err)

			got := NegotiationParams{}
			require.NoError(t, got.UnmarshalKeyValues(marshaled))

			assert.Equal(t, tt.params, got)
		})
	}
}

func TestNegotiationParams_UnmarshalKeyValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		keyvals map[string]string
		want    NegotiationParams
	}{
		{
			name:    "empty",
			keyvals: map[string]string{},
			want:    NegotiationParams{},
		},
		{
			name: "filled fields",
			keyvals: map[string]string{
				"enc":       "proto",
				"comp":      "context-takeover",
				"clevel":    "9",
				"cwinbits":  "16",
				"tid":       "f5dabdfc-17e7-4e29-8ca4-dfba8f4e719d",
				"reconnect": "true",
			},
			want: NegotiationParams{
				Encoding:           EncodingNameProtobuf,
				Compress:           compress.TypeContextTakeOver,
				CompressLevel:      pointer.ToInt(9),
				CompressWindowBits: pointer.ToInt(16),
				TransportID:        "f5dabdfc-17e7-4e29-8ca4-dfba8f4e719d",
				Reconnect:          true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NegotiationParams{}
			err := got.UnmarshalKeyValues(tt.keyvals)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNegotiationParams_MarshalKeyValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		params NegotiationParams
		want   map[string]string
	}{
		{
			name:   "default values",
			params: NegotiationParams{},
			want:   map[string]string{},
		},
		{
			name: "filled fields",
			params: NegotiationParams{
				Encoding:           EncodingNameProtobuf,
				Compress:           compress.TypeContextTakeOver,
				CompressLevel:      pointer.ToInt(9),
				CompressWindowBits: pointer.ToInt(16),
				TransportID:        "5b9417fc-dd27-4e7c-9601-56e1b603fe91",
				Reconnect:          true,
			},
			want: map[string]string{
				"enc":       "proto",
				"comp":      "context-takeover",
				"clevel":    "9",
				"cwinbits":  "16",
				"tid":       "5b9417fc-dd27-4e7c-9601-56e1b603fe91",
				"reconnect": "true",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.params.MarshalKeyValues()
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNegotiationParams_CompressConfig(t *testing.T) {
	type args struct {
		base compress.Config
	}
	tests := []struct {
		name   string
		params NegotiationParams
		args   args
		want   compress.Config
	}{
		{
			name: "overwrite default values",
			params: NegotiationParams{
				Compress:           compress.TypePerMessage,
				CompressLevel:      pointer.ToInt(9),
				CompressWindowBits: pointer.ToInt(16),
			},
			args: args{
				base: compress.Config{
					Enable:                 false,
					DisableContextTakeover: false,
					Level:                  0,
					WindowBits:             0,
				},
			},
			want: compress.Config{
				Enable:                 true,
				DisableContextTakeover: true,
				Level:                  9,
				WindowBits:             16,
			},
		},
		{
			name:   "do nothing",
			params: NegotiationParams{},
			args: args{
				base: compress.Config{
					Enable:                 true,
					DisableContextTakeover: false,
					Level:                  0,
					WindowBits:             0,
				},
			},
			want: compress.Config{
				Enable:                 false,
				DisableContextTakeover: false,
				Level:                  0,
				WindowBits:             0,
			},
		},
		{
			name:   "do nothing on default values base",
			params: NegotiationParams{},
			args: args{
				base: compress.Config{},
			},
			want: compress.Config{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.params.CompressConfig(tt.args.base))
		})
	}
}
