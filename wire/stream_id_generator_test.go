package wire_test

import (
	"math"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"

	. "github.com/aptpod/iscp-go/wire"
)

func Test_streamIDAliasGenerator_Next(t *testing.T) {
	testee := NewAliasGenerator(0)
	assert.Equal(t, uint32(1), testee.Next())
	assert.Equal(t, uint32(2), testee.Next())
}

func Test_streamIDAliasGenerator_Next_MaxUint32(t *testing.T) {
	testee := NewAliasGenerator(math.MaxUint32)
	assert.Equal(t, uint32(1), testee.Next())
	assert.Equal(t, uint32(2), testee.Next())
}

func Test_streamIDAliasGenerator_Paralell(t *testing.T) {
	testee := NewAliasGenerator(math.MaxUint32)
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
