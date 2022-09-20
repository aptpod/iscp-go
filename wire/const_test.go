package wire

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSize_String(t *testing.T) {
	tests := []struct {
		name string
		s    Size
		want string
	}{
		{name: "zero", s: 0, want: "0[b]"},
		{name: "B", s: B, want: "1.00[b]"},
		{name: "KB", s: KB, want: "1.00[kb]"},
		{name: "MB", s: MB, want: "1.00[mb]"},
		{name: "GB", s: GB, want: "1.00[gb]"},
		{name: "GB", s: GB, want: "1.00[gb]"},
		{name: "TB", s: TB, want: "1.00[tb]"},
		{name: "PB", s: PB, want: "1.00[pb]"},
		{name: "1.12PB", s: PB + (120 * TB), want: "1.12[pb]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.s.String())
		})
	}
}
