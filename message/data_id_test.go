package message_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	. "github.com/aptpod/iscp-go/message"
)

func TestMustParseDataID(t *testing.T) {
	type args struct {
		str string
	}
	tests := []struct {
		name string
		args args
		want *DataID
	}{
		{
			name: "success",
			args: args{
				str: "aaa:bbb",
			},
			want: &DataID{
				Name: "bbb",
				Type: "aaa",
			},
		},
		{
			name: "success",
			args: args{
				str: ":",
			},
			want: &DataID{
				Name: "",
				Type: "",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MustParseDataID(tt.args.str)
			assert.Equal(t, tt.want, got)
			got2, err := got.MarshalText()
			require.NoError(t, err)
			assert.Equal(t, tt.args.str, string(got2))
		})
	}
}

func TestMustParseDataID_error(t *testing.T) {
	type args struct {
		str string
	}
	tests := []struct {
		name string
		args args
	}{
		{name: "success", args: args{str: "::"}},
		{name: "success", args: args{str: ""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Panics(t, func() {
				MustParseDataID(tt.args.str)
			})
		})
	}
}
