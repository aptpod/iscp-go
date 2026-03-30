package transport

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/aptpod/iscp-go/v2/message"
)

func TestCounterAdd(t *testing.T) {
	msg1 := &message.UpstreamOpenRequest{RequestID: 1}
	msg2 := &message.UpstreamOpenRequest{RequestID: 2}
	msg3 := &message.DownstreamOpenRequest{RequestID: 3}
	c := newCounter()

	c.Add(msg1, 10)
	got := c.Count()
	assert.Equal(t, map[string]uint64{"UpstreamOpenRequest": 10}, got.ByteCount)
	assert.Equal(t, map[string]uint64{"UpstreamOpenRequest": 1}, got.MessageCount)

	c.Add(msg2, 10) // 同じ型のメッセージ → 累積
	got = c.Count()
	assert.Equal(t, map[string]uint64{"UpstreamOpenRequest": 20}, got.ByteCount)
	assert.Equal(t, map[string]uint64{"UpstreamOpenRequest": 2}, got.MessageCount)

	c.Add(msg3, 10) // 別の型のメッセージ → 新規エントリ
	got = c.Count()
	assert.Equal(t, map[string]uint64{"UpstreamOpenRequest": 20, "DownstreamOpenRequest": 10}, got.ByteCount)
	assert.Equal(t, map[string]uint64{"UpstreamOpenRequest": 2, "DownstreamOpenRequest": 1}, got.MessageCount)
}

func TestCounterConcurrent(t *testing.T) {
	c := newCounter()
	msg := &message.Ping{RequestID: 1}
	const goroutines = 10
	const iterations = 1000

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				c.Add(msg, 8)
			}
		}()
	}
	wg.Wait()

	got := c.Count()
	assert.Equal(t, uint64(goroutines*iterations), got.MessageCount["Ping"])
	assert.Equal(t, uint64(goroutines*iterations*8), got.ByteCount["Ping"])
}
