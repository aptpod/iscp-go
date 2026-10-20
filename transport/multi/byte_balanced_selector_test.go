package multi_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aptpod/iscp-go/v2/transport"
	. "github.com/aptpod/iscp-go/v2/transport/multi"
)

func TestSelectTransportByteBalanced(t *testing.T) {
	tests := []struct {
		name         string
		transportIDs []transport.SubConnectionID
		txBytes      map[transport.SubConnectionID]uint64
		state        *ByteBalancedState
		want         transport.SubConnectionID
	}{
		{
			name:         "empty returns empty",
			transportIDs: []transport.SubConnectionID{},
			txBytes:      map[transport.SubConnectionID]uint64{},
			state:        NewByteBalancedState(),
			want:         "",
		},
		{
			name:         "single transport",
			transportIDs: []transport.SubConnectionID{"t1"},
			txBytes:      map[transport.SubConnectionID]uint64{"t1": 100},
			state:        NewByteBalancedState(),
			want:         "t1",
		},
		{
			name:         "selects minimum tx bytes",
			transportIDs: []transport.SubConnectionID{"t1", "t2", "t3"},
			txBytes:      map[transport.SubConnectionID]uint64{"t1": 300, "t2": 100, "t3": 200},
			state:        NewByteBalancedState(),
			want:         "t2",
		},
		{
			name:         "equal bytes selects first in list",
			transportIDs: []transport.SubConnectionID{"t1", "t2"},
			txBytes:      map[transport.SubConnectionID]uint64{"t1": 100, "t2": 100},
			state:        NewByteBalancedState(),
			want:         "t1",
		},
		{
			name:         "nil state does not record stats",
			transportIDs: []transport.SubConnectionID{"t1", "t2"},
			txBytes:      map[transport.SubConnectionID]uint64{"t1": 300, "t2": 100},
			state:        nil,
			want:         "t2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getTxBytes := func(id transport.SubConnectionID) uint64 {
				return tt.txBytes[id]
			}
			got := SelectTransportByteBalanced(tt.transportIDs, getTxBytes, tt.state)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSelectTransportByteBalanced_StatsRecording(t *testing.T) {
	state := NewByteBalancedState()
	txBytes := map[transport.SubConnectionID]uint64{"t1": 300, "t2": 100}
	getTxBytes := func(id transport.SubConnectionID) uint64 { return txBytes[id] }

	// Select t2 three times
	for range 3 {
		SelectTransportByteBalanced([]transport.SubConnectionID{"t1", "t2"}, getTxBytes, state)
	}

	assert.Equal(t, uint64(3), state.TotalSelections.Load())

	state.SelectionCountsMu.Lock()
	assert.Equal(t, uint64(3), state.SelectionCounts["t2"])
	state.SelectionCountsMu.Unlock()

	// Switch to t1
	txBytes["t2"] = 500
	SelectTransportByteBalanced([]transport.SubConnectionID{"t1", "t2"}, getTxBytes, state)
	assert.Equal(t, uint64(1), state.SwitchCount.Load())
}

func TestByteBalancedSelector_NewByteBalancedSelector(t *testing.T) {
	transportIDs := []transport.SubConnectionID{"t1", "t2", "t3"}
	selector := NewByteBalancedSelector(transportIDs)

	require.NotNil(t, selector)
}

func TestByteBalancedSelector_SetMultiTransport(t *testing.T) {
	selector := NewByteBalancedSelector([]transport.SubConnectionID{"t1"})

	// Verify that setting nil does not panic
	selector.SetMultiTransport(nil)
}

func TestByteBalancedSelector_Get_EmptySubConnectionIDs(t *testing.T) {
	selector := NewByteBalancedSelector([]transport.SubConnectionID{})

	// Returns an empty string when empty
	assert.Equal(t, transport.SubConnectionID(""), selector.Get(context.Background(), 0))
}

func TestByteBalancedSelector_Get_SingleSubConnectionID(t *testing.T) {
	selector := NewByteBalancedSelector([]transport.SubConnectionID{"t1"})

	// With a single transport, returns it
	assert.Equal(t, transport.SubConnectionID("t1"), selector.Get(context.Background(), 0))
	assert.Equal(t, transport.SubConnectionID("t1"), selector.Get(context.Background(), 0))
	assert.Equal(t, transport.SubConnectionID("t1"), selector.Get(context.Background(), 0))
}

func TestByteBalancedSelector_Get_MultipleSubConnectionIDs_WithoutMultiTransport(t *testing.T) {
	transportIDs := []transport.SubConnectionID{"t1", "t2", "t3"}
	selector := NewByteBalancedSelector(transportIDs)

	// When multiTransport is not set, returns the first transport
	assert.Equal(t, transport.SubConnectionID("t1"), selector.Get(context.Background(), 0))
	assert.Equal(t, transport.SubConnectionID("t1"), selector.Get(context.Background(), 0))
}

func TestByteBalancedSelector_Stats(t *testing.T) {
	selector := NewByteBalancedSelector([]transport.SubConnectionID{"t1"})

	// Select 5 times
	for range 5 {
		selector.Get(context.Background(), 0)
	}

	stats := selector.Stats()
	assert.Equal(t, uint64(5), stats.TotalSelections)
	assert.Equal(t, uint64(5), stats.SelectionCounts["t1"])
	assert.Equal(t, uint64(0), stats.SwitchCount) // Same transport, so no switch
}

func TestByteBalancedSelector_ResetStats(t *testing.T) {
	selector := NewByteBalancedSelector([]transport.SubConnectionID{"t1"})

	// Run selection
	for range 5 {
		selector.Get(context.Background(), 0)
	}

	// Reset
	selector.ResetStats()

	stats := selector.Stats()
	assert.Equal(t, uint64(0), stats.TotalSelections)
	assert.Equal(t, uint64(0), stats.SwitchCount)
	assert.Empty(t, stats.SelectionCounts)
}

func TestByteBalancedSelector_TransportSelectorInterface(t *testing.T) {
	selector := NewByteBalancedSelector([]transport.SubConnectionID{"t1"})

	// Verify that it satisfies the TransportSelector interface
	var _ TransportSelector = selector
}

func TestByteBalancedSelector_MultiTransportSetterInterface(t *testing.T) {
	selector := NewByteBalancedSelector([]transport.SubConnectionID{"t1"})

	// Verify that it satisfies the MultiTransportSetter interface
	var _ MultiTransportSetter = selector
}

func TestByteBalancedSelector_ConcurrentAccess_Get(t *testing.T) {
	transportIDs := []transport.SubConnectionID{"t1", "t2", "t3"}
	selector := NewByteBalancedSelector(transportIDs)

	var wg sync.WaitGroup

	// Concurrent Get calls from multiple goroutines
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				selector.Get(context.Background(), 0)
			}
		}()
	}

	wg.Wait()
}

func TestByteBalancedSelector_ConcurrentAccess_SetMultiTransportAndGet(t *testing.T) {
	transportIDs := []transport.SubConnectionID{"t1", "t2"}
	selector := NewByteBalancedSelector(transportIDs)

	var wg sync.WaitGroup

	// Concurrent Get and SetMultiTransport
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 100 {
			selector.Get(context.Background(), 0)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 100 {
			selector.SetMultiTransport(nil)
		}
	}()

	wg.Wait()
}

func TestByteBalancedSelector_ConcurrentAccess_StatsAndGet(t *testing.T) {
	transportIDs := []transport.SubConnectionID{"t1", "t2"}
	selector := NewByteBalancedSelector(transportIDs)

	var wg sync.WaitGroup

	// Concurrent Get and Stats
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 100 {
			selector.Get(context.Background(), 0)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 100 {
			_ = selector.Stats()
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 100 {
			selector.ResetStats()
		}
	}()

	wg.Wait()
}
