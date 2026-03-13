package segment_test

import (
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	. "github.com/aptpod/iscp-go/internal/segment"
	"github.com/aptpod/iscp-go/transport"
)

type RecordSender struct {
	Captured [][]byte
}

func (s *RecordSender) SendDatagram(bs []byte) error {
	s.Captured = append(s.Captured, bs)
	return nil
}

func Test_sendTo(t *testing.T) {
	type args struct {
		seqNum     uint32
		msgPayload []byte
	}
	tests := []struct {
		name      string
		args      args
		want      int
		wantBytes [][]byte
		wantError error
	}{
		{
			name: "success",
			args: args{
				seqNum:     1,
				msgPayload: []byte{0, 1, 2, 3, 4, 5, 6, 7},
			},
			want: 16,
			wantBytes: [][]byte{
				{
					0, 0, 0, 1,
					0, 0,
					0, 0,
					0, 1, 2, 3, 4, 5, 6, 7,
				},
			},
		},
		{
			name: "success: segmentation",
			args: args{
				seqNum:     1,
				msgPayload: []byte{0, 1, 2, 3, 4, 5, 6, 7, 8},
			},
			want: 25,
			wantBytes: [][]byte{
				{
					0, 0, 0, 1,
					0, 1,
					0, 0,
					0, 1, 2, 3, 4, 5, 6, 7,
				},
				{
					0, 0, 0, 1,
					0, 1,
					0, 1,
					8,
				},
			},
		},
		{
			name: "invalid message",
			args: args{
				seqNum:     1,
				msgPayload: []byte(strings.Repeat("a", 8*(math.MaxUint16+1))),
			},
			wantError: transport.ErrInvalidMessage,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			SetMaxPayloadSize(t, 8)
			sender := &RecordSender{}
			got, err := SendTo(sender, tt.args.seqNum, tt.args.msgPayload)
			assert.ErrorIs(t, err, tt.wantError)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantBytes, sender.Captured)
		})
	}
}
