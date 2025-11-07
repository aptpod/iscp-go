package encoding_test

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"

	. "github.com/aptpod/iscp-go/encoding"
	"github.com/aptpod/iscp-go/message"
)

func Test_counter_Add(t *testing.T) {
	msg1 := &message.UpstreamOpenRequest{RequestID: 1}
	msg2 := &message.UpstreamOpenRequest{RequestID: 2}
	msg3 := &message.DownstreamOpenRequest{RequestID: 3}
	c := NewCounter()
	c.Add(msg1, 10)
	assert.Equal(t, &Count{
		ByteCount: map[reflect.Type]uint64{
			reflect.TypeOf(msg1): 10,
		},
		MessageCount: map[reflect.Type]uint64{
			reflect.TypeOf(msg1): 1,
		},
	}, c.Count())
	c.Add(msg2, 10)
	assert.Equal(t, &Count{
		ByteCount: map[reflect.Type]uint64{
			reflect.TypeOf(msg1): 20,
		},
		MessageCount: map[reflect.Type]uint64{
			reflect.TypeOf(msg1): 2,
		},
	}, c.Count())
	c.Add(msg3, 10)
	assert.Equal(t, &Count{
		ByteCount: map[reflect.Type]uint64{
			reflect.TypeOf(msg1): 20,
			reflect.TypeOf(msg3): 10,
		},
		MessageCount: map[reflect.Type]uint64{
			reflect.TypeOf(msg1): 2,
			reflect.TypeOf(msg3): 1,
		},
	}, c.Count())
}
