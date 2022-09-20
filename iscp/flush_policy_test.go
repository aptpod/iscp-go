package iscp_test

import (
	"fmt"
	"testing"
	"time"

	. "github.com/aptpod/iscp-go/iscp"
	"github.com/stretchr/testify/assert"
)

func Test_FlushPolicy(t *testing.T) {
	tests := []struct {
		testee      FlushPolicy
		argsSize    uint32
		wantIsFlush bool
		wantTick    bool
	}{
		{testee: &FlushPolicyNone{}, argsSize: 100, wantIsFlush: false, wantTick: false},
		{testee: &FlushPolicyIntervalOnly{Interval: time.Microsecond * 900}, argsSize: 100, wantIsFlush: false, wantTick: true},
		{testee: &FlushPolicyBufferSizeOnly{BufferSize: 100}, argsSize: 101, wantIsFlush: true, wantTick: false},
		{testee: &FlushPolicyBufferSizeOnly{BufferSize: 100}, argsSize: 100, wantIsFlush: false, wantTick: false},
		{testee: &FlushPolicyBufferSizeOnly{BufferSize: 100}, argsSize: 100, wantIsFlush: false, wantTick: false},
		{testee: &FlushPolicyImmediately{}, argsSize: 0, wantIsFlush: true, wantTick: false},
		{testee: &FlushPolicyIntervalOrBufferSize{
			BufferPolicy:   &FlushPolicyBufferSizeOnly{BufferSize: 100},
			IntervalPolicy: &FlushPolicyIntervalOnly{Interval: time.Microsecond * 1100},
		}, argsSize: 101, wantIsFlush: true, wantTick: false},
		{testee: &FlushPolicyIntervalOrBufferSize{
			BufferPolicy:   &FlushPolicyBufferSizeOnly{BufferSize: 101},
			IntervalPolicy: &FlushPolicyIntervalOnly{Interval: time.Microsecond * 900},
		}, argsSize: 101, wantIsFlush: false, wantTick: true},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%T", tt.testee), func(t *testing.T) {
			t.Run("IsFlush", func(t *testing.T) {
				assert.Equal(t, tt.wantIsFlush, tt.testee.IsFlush(tt.argsSize))
			})
			t.Run("Ticker", func(t *testing.T) {
				c, f := tt.testee.Ticker()
				defer f()
				select {
				case <-c:
					assert.True(t, tt.wantTick)
				case <-time.After(time.Millisecond):
					assert.False(t, tt.wantTick)
				}
			})
		})
	}
}
