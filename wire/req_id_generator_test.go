package wire_test

import (
	"math"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	. "github.com/aptpod/iscp-go/wire"
)

func Test_idGenerator_Next(t *testing.T) {
	g := NewIDGenerator(1)
	for i := 0; i < 512; i++ {
		require.Equal(t, uint32(1+(i*2)), g.Next())
	}

	g = NewIDGenerator(2)
	for i := 0; i < 512; i++ {
		require.Equal(t, uint32(2+(i*2)), g.Next())
	}

	g = NewIDGenerator(math.MaxUint32)
	require.Equal(t, uint32(math.MaxUint32), g.Next())
	require.Equal(t, 1, math.MaxUint32%2)
	require.Equal(t, uint32(1), g.Next())
}

func Test_idGenerator_Parallel(t *testing.T) {
	testee := NewIDGenerator(math.MaxUint32)
	var mu sync.Mutex
	gots := map[uint32]struct{}{}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100000; i++ {
			got := testee.Next()
			mu.Lock()
			gots[got] = struct{}{}
			mu.Unlock()
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100000; i++ {
			got := testee.Next()
			mu.Lock()
			gots[got] = struct{}{}
			mu.Unlock()
		}
	}()
	wg.Wait()
	assert.Equal(t, 200000, len(gots))
	assert.NotContains(t, gots, uint32(0))
	assert.Contains(t, gots, uint32(1))
}
