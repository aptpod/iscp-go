package json_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	. "github.com/aptpod/iscp-go/v2/encoding/json"
	"github.com/aptpod/iscp-go/v2/message"
)

// These tests pin the protojson behaviors documented in MIGRATION.md.

func Test_encoder_EncodeTo_protojsonBehaviors(t *testing.T) {
	tests := []struct {
		name     string
		in       *message.UpstreamCall
		contains string
		wantErr  bool
	}{
		{name: "nil bytes are encoded as empty string", in: &message.UpstreamCall{Name: "n"}, contains: `"payload":""`},
		{name: "html characters are not escaped", in: &message.UpstreamCall{Name: "a<b>&"}, contains: `a<b>&`},
		{name: "invalid utf8 is rejected", in: &message.UpstreamCall{Name: "\xff"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.in.ExtensionFields = &message.UpstreamCallExtensionFields{}
			var buf bytes.Buffer
			_, err := NewEncoding().EncodeTo(&buf, tt.in)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Contains(t, strings.ReplaceAll(buf.String(), " ", ""), tt.contains)
		})
	}
}

func Test_encoder_DecodeFrom_protojsonBehaviors(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr bool
		check   func(t *testing.T, m message.Message)
	}{
		{
			name: "empty bytes are decoded as nil",
			in:   `{"upstream_call":{"name":"n","payload":"","extension_fields":{}}}`,
			check: func(t *testing.T, m message.Message) {
				assert.Nil(t, m.(*message.UpstreamCall).Payload)
			},
		},
		{
			name: "empty downstream_filter_references are decoded as nil",
			in:   `{"downstream_chunk":{"stream_id_alias":1,"upstream_alias":2,"stream_chunk":{"sequence_number":3,"data_point_groups":[]},"extension_fields":{},"downstream_filter_references":[]}}`,
			check: func(t *testing.T, m message.Message) {
				assert.Nil(t, m.(*message.DownstreamChunk).DownstreamFilterReferences)
			},
		},
		{name: "duplicate keys are rejected", in: `{"upstream_call":{"name":"a","name":"b"}}`, wantErr: true},
		{name: "trailing data is rejected", in: `{"upstream_call":{"name":"a"}}{}`, wantErr: true},
		{name: "unknown fields are rejected", in: `{"upstream_call":{"unknown":1}}`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, m, err := NewEncoding().DecodeFrom(strings.NewReader(tt.in))
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			tt.check(t, m)
		})
	}
}
