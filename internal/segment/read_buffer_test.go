package segment_test

import (
	"sync"
	"testing"
	"time"

	. "github.com/aptpod/iscp-go/internal/segment"
	"github.com/stretchr/testify/assert"
)

func Test_readUnreliableBuffers_Restore(t *testing.T) {
	type fields struct {
		readUnreliableBuffer       map[uint32]*ReadBuffer
		readUnreliableBufferExpiry time.Duration
	}
	type args struct {
		bs []byte
	}
	tests := []struct {
		name      string
		fields    fields
		args      args
		want      []byte
		want1     bool
		wantMsgs  map[uint32]*ReadBuffer
		assertion assert.ErrorAssertionFunc
	}{
		{
			name: "success initial state",
			fields: fields{
				readUnreliableBuffer:       map[uint32]*ReadBuffer{},
				readUnreliableBufferExpiry: time.Second,
			},
			args: args{
				bs: []byte{
					0, 0, 0, 1, // seqnum
					0, 2, // max seg
					0, 1, // seg
					2, 2, 3, 4, 5, // payload
				},
			},
			want:      nil,
			want1:     false,
			assertion: assert.NoError,
			wantMsgs: map[uint32]*ReadBuffer{
				1: {
					SegCount: 1,
					MsgSize:  5,
					Msgs: [][]byte{
						nil,
						{2, 2, 3, 4, 5},
						nil,
					},
					ExpiredAt: time.Unix(2, 0).UTC(),
				},
			},
		},
		{
			name: "success building",
			fields: fields{
				readUnreliableBuffer: map[uint32]*ReadBuffer{
					1: {
						SegCount: 1,
						MsgSize:  5,
						Msgs: [][]byte{
							{1, 2, 3, 4, 5},
							nil,
							nil,
						},
						ExpiredAt: time.Time{},
					},
				},
				readUnreliableBufferExpiry: time.Second,
			},
			args: args{
				bs: []byte{
					0, 0, 0, 1, // seqnum
					0, 2, // max seg
					0, 1, // seg
					2, 2, 3, 4, 5, // payload
				},
			},
			want:      nil,
			want1:     false,
			assertion: assert.NoError,
			wantMsgs: map[uint32]*ReadBuffer{
				1: {
					SegCount: 2,
					MsgSize:  10,
					Msgs: [][]byte{
						{1, 2, 3, 4, 5},
						{2, 2, 3, 4, 5},
						nil,
					},
					ExpiredAt: time.Unix(2, 0).UTC(),
				},
			},
		},
		{
			name: "success finished",
			fields: fields{
				readUnreliableBuffer: map[uint32]*ReadBuffer{
					1: {
						SegCount: 2,
						MsgSize:  10,
						Msgs: [][]byte{
							{1, 2, 3, 4, 5},
							{2, 2, 3, 4, 5},
							nil,
						},
						ExpiredAt: time.Unix(2, 0).UTC(),
					},
				},
				readUnreliableBufferExpiry: time.Second,
			},
			args: args{
				bs: []byte{
					0, 0, 0, 1, // seqnum
					0, 2, // max seg
					0, 2, // seg
					3, 2, 3, 4, 5, // payload
				},
			},
			want: []byte{
				1, 2, 3, 4, 5,
				2, 2, 3, 4, 5,
				3, 2, 3, 4, 5,
			},
			want1:     true,
			assertion: assert.NoError,
			wantMsgs:  map[uint32]*ReadBuffer{},
		},
		{
			name: "invalid seg idx",
			fields: fields{
				readUnreliableBuffer: map[uint32]*ReadBuffer{
					1: {
						SegCount: 2,
						MsgSize:  10,
						Msgs: [][]byte{
							{1, 2, 3, 4, 5},
							{2, 2, 3, 4, 5},
							nil,
						},
						ExpiredAt: time.Unix(2, 0).UTC(),
					},
				},
				readUnreliableBufferExpiry: time.Second,
			},
			args: args{
				bs: []byte{
					0, 0, 0, 1, // seqnum
					0, 2, // max seg
					0, 3, // seg
					3, 2, 3, 4, 5, // payload
				},
			},
			want:      nil,
			want1:     false,
			assertion: assert.NoError,
			wantMsgs: map[uint32]*ReadBuffer{
				1: {
					SegCount: 2,
					MsgSize:  10,
					Msgs: [][]byte{
						{1, 2, 3, 4, 5},
						{2, 2, 3, 4, 5},
						nil,
					},
					ExpiredAt: time.Unix(2, 0).UTC(),
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			SetTimeNow(t, time.Unix(1, 0).UTC())
			tr := &ReadBuffers{
				Mutex:            sync.Mutex{},
				ReadBuffer:       tt.fields.readUnreliableBuffer,
				ReadBufferExpiry: tt.fields.readUnreliableBufferExpiry,
			}
			got, got1, err := tr.Receive(tt.args.bs)
			tt.assertion(t, err)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.want1, got1)
			assert.Equal(t, tt.wantMsgs, tr.ReadBuffer)
		})
	}
}

func Test_readUnreliableBuffers_RemoveExpired(t *testing.T) {
	type fields struct {
		readUnreliableBuffer       map[uint32]*ReadBuffer
		readUnreliableBufferExpiry time.Duration
	}
	tests := []struct {
		name     string
		fields   fields
		wantMsgs map[uint32]*ReadBuffer
	}{
		{
			name: "success",
			fields: fields{
				readUnreliableBuffer: map[uint32]*ReadBuffer{
					1: {
						SegCount: 1,
						MsgSize:  5,
						Msgs: [][]byte{
							{1, 2, 3, 4, 5},
							nil,
						},
						ExpiredAt: time.Unix(2, 0).UTC(),
					},
					2: {
						SegCount: 1,
						MsgSize:  5,
						Msgs: [][]byte{
							{1, 2, 3, 4, 5},
							nil,
						},
						ExpiredAt: time.Unix(3, 0).UTC(),
					},
				},
				readUnreliableBufferExpiry: time.Second,
			},
			wantMsgs: map[uint32]*ReadBuffer{
				1: {
					SegCount: 1,
					MsgSize:  5,
					Msgs: [][]byte{
						{1, 2, 3, 4, 5},
						nil,
					},
					ExpiredAt: time.Unix(2, 0).UTC(),
				},
				2: {
					SegCount: 1,
					MsgSize:  5,
					Msgs: [][]byte{
						{1, 2, 3, 4, 5},
						nil,
					},
					ExpiredAt: time.Unix(3, 0).UTC(),
				},
			},
		},
		{
			name: "success",
			fields: fields{
				readUnreliableBuffer: map[uint32]*ReadBuffer{
					1: {
						SegCount: 1,
						MsgSize:  5,
						Msgs: [][]byte{
							{1, 2, 3, 4, 5},
							nil,
						},
						ExpiredAt: time.Unix(1, 0).UTC(),
					},
					2: {
						SegCount: 1,
						MsgSize:  5,
						Msgs: [][]byte{
							{1, 2, 3, 4, 5},
							nil,
						},
						ExpiredAt: time.Unix(2, 0).UTC(),
					},
				},
				readUnreliableBufferExpiry: time.Second,
			},
			wantMsgs: map[uint32]*ReadBuffer{
				2: {
					SegCount: 1,
					MsgSize:  5,
					Msgs: [][]byte{
						{1, 2, 3, 4, 5},
						nil,
					},
					ExpiredAt: time.Unix(2, 0).UTC(),
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			SetTimeNow(t, time.Unix(2, 0).UTC())
			tr := &ReadBuffers{
				Mutex:            sync.Mutex{},
				ReadBuffer:       tt.fields.readUnreliableBuffer,
				ReadBufferExpiry: tt.fields.readUnreliableBufferExpiry,
			}
			tr.RemoveExpired()
			assert.Equal(t, tt.wantMsgs, tr.ReadBuffer)
		})
	}
}
