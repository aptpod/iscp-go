package multi_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aptpod/iscp-go/v2/log"
	"github.com/aptpod/iscp-go/v2/transport"
	. "github.com/aptpod/iscp-go/v2/transport/multi"
)

func TestMinRTTSelector_NewMinRTTSelector(t *testing.T) {
	selector := NewMinRTTSelector()

	require.NotNil(t, selector)
	// Calling Get() in the initial state returns an empty string
	assert.Equal(t, transport.SubConnectionID(""), selector.Get(context.Background(), 1000))
}

func TestMinRTTSelector_SetMultiTransport(t *testing.T) {
	selector := NewMinRTTSelector()

	// Verify that setting nil does not panic
	selector.SetMultiTransport(nil)
}

func TestMinRTTSelector_SetLogger(t *testing.T) {
	selector := NewMinRTTSelector()

	logger := log.NewNop()
	// Verify that it does not panic
	selector.SetLogger(logger)
}

func TestMinRTTSelector_SetQueueSize(t *testing.T) {
	selector := NewMinRTTSelector()

	// It is a no-op, so only verify that it does not panic
	selector.SetQueueSize(1000)
	selector.SetQueueSize(0)
}

func TestMinRTTSelector_UpdateTransport(t *testing.T) {
	selector := NewMinRTTSelector()

	transportID := transport.SubConnectionID("test-transport")
	provider := &mockMetricsProvider{
		rtt:              50 * time.Millisecond,
		rttvar:           25 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000,
	}

	info := NewTransportInfo(transportID, provider)
	selector.UpdateTransport(transportID, info)

	// Verify with Get that the transport is registered
	selectedID := selector.Get(context.Background(), 1000)
	assert.Equal(t, transportID, selectedID)
}

func TestMinRTTSelector_UpdateTransport_PreservesMinRTT(t *testing.T) {
	// How MinRTT is retained is an internal implementation detail, so only verify that it works
	selector := NewMinRTTSelector()

	transportID := transport.SubConnectionID("test-transport")

	// First update
	provider1 := &mockMetricsProvider{
		rtt:              30 * time.Millisecond,
		rttvar:           15 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000,
	}
	info1 := NewTransportInfo(transportID, provider1)
	selector.UpdateTransport(transportID, info1)

	// Verify that it can be selected
	selectedID := selector.Get(context.Background(), 1000)
	assert.Equal(t, transportID, selectedID)

	// Second update (the RTT becomes larger)
	provider2 := &mockMetricsProvider{
		rtt:              100 * time.Millisecond,
		rttvar:           50 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000,
	}
	info2 := NewTransportInfo(transportID, provider2)
	selector.UpdateTransport(transportID, info2)

	// Verify that it can still be selected
	selectedID = selector.Get(context.Background(), 1000)
	assert.Equal(t, transportID, selectedID)
}

func TestMinRTTSelector_Get(t *testing.T) {
	tests := []struct {
		name      string
		setup     func() *MinRTTSelector
		want      transport.SubConnectionID
		wantOneOf []transport.SubConnectionID
	}{
		{
			name: "edge case: no transports returns empty",
			setup: func() *MinRTTSelector {
				return NewMinRTTSelector()
			},
			want: "",
		},
		{
			name: "single transport returns that transport",
			setup: func() *MinRTTSelector {
				selector := NewMinRTTSelector()
				id := transport.SubConnectionID("only")
				provider := &mockMetricsProvider{
					rtt:              50 * time.Millisecond,
					rttvar:           25 * time.Millisecond,
					congestionWindow: 20000,
					bytesInFlight:    5000,
				}
				selector.UpdateTransport(id, NewTransportInfo(id, provider))
				return selector
			},
			want: "only",
		},
		{
			name: "selects transport with minimum RTT",
			setup: func() *MinRTTSelector {
				selector := NewMinRTTSelector()

				// Fast transport
				fast := transport.SubConnectionID("fast")
				fastProvider := &mockMetricsProvider{
					rtt:              20 * time.Millisecond,
					rttvar:           10 * time.Millisecond,
					congestionWindow: 20000,
					bytesInFlight:    5000,
				}
				selector.UpdateTransport(fast, NewTransportInfo(fast, fastProvider))

				// Slow transport
				slow := transport.SubConnectionID("slow")
				slowProvider := &mockMetricsProvider{
					rtt:              100 * time.Millisecond,
					rttvar:           50 * time.Millisecond,
					congestionWindow: 20000,
					bytesInFlight:    5000,
				}
				selector.UpdateTransport(slow, NewTransportInfo(slow, slowProvider))

				return selector
			},
			want: "fast",
		},
		{
			name: "skips transport not sending allowed and selects available minimum RTT",
			setup: func() *MinRTTSelector {
				selector := NewMinRTTSelector()

				// Fast but cannot send (CWND saturated)
				fast := transport.SubConnectionID("fast")
				fastProvider := &mockMetricsProvider{
					rtt:              20 * time.Millisecond,
					rttvar:           10 * time.Millisecond,
					congestionWindow: 10000,
					bytesInFlight:    10000, // CWND == BytesInFlight
				}
				selector.UpdateTransport(fast, NewTransportInfo(fast, fastProvider))

				// Slow but can send
				slow := transport.SubConnectionID("slow")
				slowProvider := &mockMetricsProvider{
					rtt:              100 * time.Millisecond,
					rttvar:           50 * time.Millisecond,
					congestionWindow: 20000,
					bytesInFlight:    5000,
				}
				selector.UpdateTransport(slow, NewTransportInfo(slow, slowProvider))

				return selector
			},
			want: "slow",
		},
		{
			name: "all transports not sending allowed falls back to minimum RTT among all",
			setup: func() *MinRTTSelector {
				selector := NewMinRTTSelector()

				// Fast but cannot send
				fast := transport.SubConnectionID("fast")
				fastProvider := &mockMetricsProvider{
					rtt:              20 * time.Millisecond,
					rttvar:           10 * time.Millisecond,
					congestionWindow: 10000,
					bytesInFlight:    10000,
				}
				selector.UpdateTransport(fast, NewTransportInfo(fast, fastProvider))

				// Slow and cannot send
				slow := transport.SubConnectionID("slow")
				slowProvider := &mockMetricsProvider{
					rtt:              100 * time.Millisecond,
					rttvar:           50 * time.Millisecond,
					congestionWindow: 10000,
					bytesInFlight:    10000,
				}
				selector.UpdateTransport(slow, NewTransportInfo(slow, slowProvider))

				return selector
			},
			want: "fast",
		},
		{
			name: "same MinRTT uses SmoothedRTT as tiebreaker",
			setup: func() *MinRTTSelector {
				selector := NewMinRTTSelector()

				// Same MinRTT but smaller SmoothedRTT
				t1 := transport.SubConnectionID("t1")
				provider1 := &mockMetricsProvider{
					rtt:              50 * time.Millisecond,
					rttvar:           25 * time.Millisecond,
					congestionWindow: 20000,
					bytesInFlight:    5000,
				}
				selector.UpdateTransport(t1, NewTransportInfo(t1, provider1))

				// Same MinRTT but larger SmoothedRTT
				t2 := transport.SubConnectionID("t2")
				provider2 := &mockMetricsProvider{
					rtt:              80 * time.Millisecond,
					rttvar:           40 * time.Millisecond,
					congestionWindow: 20000,
					bytesInFlight:    5000,
				}
				selector.UpdateTransport(t2, NewTransportInfo(t2, provider2))

				return selector
			},
			want: "t1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selector := tt.setup()
			got := selector.Get(context.Background(), 1000)

			if len(tt.wantOneOf) > 0 {
				assert.Contains(t, tt.wantOneOf, got)
			} else {
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestMinRTTSelector_Stats(t *testing.T) {
	selector := NewMinRTTSelector()

	fast := transport.SubConnectionID("fast")
	fastProvider := &mockMetricsProvider{
		rtt:              20 * time.Millisecond,
		rttvar:           10 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000,
	}
	selector.UpdateTransport(fast, NewTransportInfo(fast, fastProvider))

	// Select 5 times
	for range 5 {
		selector.Get(context.Background(), 1000)
	}

	stats := selector.Stats()
	assert.Equal(t, uint64(5), stats.TotalSelections)
	assert.Equal(t, uint64(5), stats.SelectionCounts[fast])
	assert.Equal(t, uint64(0), stats.SwitchCount) // Same transport, so no switch
}

func TestMinRTTSelector_Stats_SwitchCount(t *testing.T) {
	selector := NewMinRTTSelector()

	fast := transport.SubConnectionID("fast")
	fastProvider := &mockMetricsProvider{
		rtt:              20 * time.Millisecond,
		rttvar:           10 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000,
	}

	slow := transport.SubConnectionID("slow")
	slowProvider := &mockMetricsProvider{
		rtt:              100 * time.Millisecond,
		rttvar:           50 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000,
	}

	// Fast first
	selector.UpdateTransport(fast, NewTransportInfo(fast, fastProvider))
	selector.Get(context.Background(), 1000) // select fast

	// Add slow (fast becomes unable to send)
	fastProvider.bytesInFlight = 20000
	fastProvider.congestionWindow = 20000
	selector.UpdateTransport(fast, NewTransportInfo(fast, fastProvider))
	selector.UpdateTransport(slow, NewTransportInfo(slow, slowProvider))
	selector.Get(context.Background(), 1000) // select slow (a switch occurs)

	stats := selector.Stats()
	assert.Equal(t, uint64(2), stats.TotalSelections)
	assert.Equal(t, uint64(1), stats.SwitchCount)
}

func TestMinRTTSelector_ResetStats(t *testing.T) {
	selector := NewMinRTTSelector()

	fast := transport.SubConnectionID("fast")
	fastProvider := &mockMetricsProvider{
		rtt:              20 * time.Millisecond,
		rttvar:           10 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000,
	}
	selector.UpdateTransport(fast, NewTransportInfo(fast, fastProvider))

	// Run selection
	for range 5 {
		selector.Get(context.Background(), 1000)
	}

	// Reset
	selector.ResetStats()

	stats := selector.Stats()
	assert.Equal(t, uint64(0), stats.TotalSelections)
	assert.Equal(t, uint64(0), stats.SwitchCount)
	assert.Empty(t, stats.SelectionCounts)
}

func TestMinRTTSelector_ConcurrentAccess(t *testing.T) {
	selector := NewMinRTTSelector()

	t1 := transport.SubConnectionID("t1")
	provider1 := &mockMetricsProvider{
		rtt:              50 * time.Millisecond,
		rttvar:           25 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000,
	}
	selector.UpdateTransport(t1, NewTransportInfo(t1, provider1))

	t2 := transport.SubConnectionID("t2")
	provider2 := &mockMetricsProvider{
		rtt:              100 * time.Millisecond,
		rttvar:           50 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000,
	}
	selector.UpdateTransport(t2, NewTransportInfo(t2, provider2))

	var wg sync.WaitGroup

	// Concurrent Get and SetQueueSize
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 100 {
			selector.Get(context.Background(), 1000)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range 100 {
			selector.SetQueueSize(uint64(i * 100))
		}
	}()

	// Concurrent Get and UpdateTransport
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 100 {
			selector.Get(context.Background(), 1000)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 100 {
			provider := &mockMetricsProvider{
				rtt:              50 * time.Millisecond,
				rttvar:           25 * time.Millisecond,
				congestionWindow: 20000,
				bytesInFlight:    5000,
			}
			selector.UpdateTransport(t1, NewTransportInfo(t1, provider))
		}
	}()

	wg.Wait()
}

func TestMinRTTSelector_TransportMetricsUpdaterInterface(t *testing.T) {
	selector := NewMinRTTSelector()

	// Verify that it satisfies the TransportMetricsUpdater interface
	var _ TransportMetricsUpdater = selector

	// Verify that UpdateTransport can be called
	transportID := transport.SubConnectionID("test-transport")
	provider := &mockMetricsProvider{
		rtt:              50 * time.Millisecond,
		rttvar:           25 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000,
	}
	info := NewTransportInfo(transportID, provider)
	selector.UpdateTransport(transportID, info)

	// Verify that SetQueueSize can be called (no-op)
	selector.SetQueueSize(1000)

	// Verify that the transport is registered
	selectedID := selector.Get(context.Background(), 1000)
	assert.Equal(t, transportID, selectedID)
}

func TestMinRTTSelector_MultiTransportSetterInterface(t *testing.T) {
	selector := NewMinRTTSelector()

	// Verify that it satisfies the MultiTransportSetter interface
	var _ MultiTransportSetter = selector

	// Verify that SetMultiTransport can be called
	selector.SetMultiTransport(nil)
}

func TestMinRTTSelector_TransportSelectorInterface(t *testing.T) {
	selector := NewMinRTTSelector()

	// Verify that it satisfies the TransportSelector interface
	var _ TransportSelector = selector
}

// newTestTransportInfo is a helper that creates a TransportInfo for tests.
func newTestTransportInfo(id transport.SubConnectionID, rtt time.Duration, cwnd, bif uint64) *TransportInfo {
	provider := &mockMetricsProvider{
		rtt:              rtt,
		rttvar:           rtt / 2,
		congestionWindow: cwnd,
		bytesInFlight:    bif,
	}
	info := NewTransportInfo(id, provider)
	info.Update()
	return info
}

func TestSelectTransportMinRTT_Standalone(t *testing.T) {
	tests := []struct {
		name       string
		transports map[transport.SubConnectionID]*TransportInfo
		state      *MinRTTState
		want       transport.SubConnectionID
	}{
		{
			name:       "empty returns empty",
			transports: map[transport.SubConnectionID]*TransportInfo{},
			state:      NewMinRTTState(),
			want:       "",
		},
		{
			name: "single transport returns it",
			transports: map[transport.SubConnectionID]*TransportInfo{
				"t1": newTestTransportInfo("t1", 50*time.Millisecond, 20000, 5000),
			},
			state: NewMinRTTState(),
			want:  "t1",
		},
		{
			name: "selects minimum RTT among sending allowed",
			transports: map[transport.SubConnectionID]*TransportInfo{
				"fast": newTestTransportInfo("fast", 20*time.Millisecond, 20000, 5000),
				"slow": newTestTransportInfo("slow", 100*time.Millisecond, 20000, 5000),
			},
			state: NewMinRTTState(),
			want:  "fast",
		},
		{
			name: "skips not sending allowed, selects available",
			transports: map[transport.SubConnectionID]*TransportInfo{
				"fast": newTestTransportInfo("fast", 20*time.Millisecond, 10000, 10000), // not sending allowed
				"slow": newTestTransportInfo("slow", 100*time.Millisecond, 20000, 5000),
			},
			state: NewMinRTTState(),
			want:  "slow",
		},
		{
			name: "all not sending allowed, falls back to min RTT",
			transports: map[transport.SubConnectionID]*TransportInfo{
				"fast": newTestTransportInfo("fast", 20*time.Millisecond, 10000, 10000),
				"slow": newTestTransportInfo("slow", 100*time.Millisecond, 10000, 10000),
			},
			state: NewMinRTTState(),
			want:  "fast",
		},
		{
			name: "nil state does not record stats",
			transports: map[transport.SubConnectionID]*TransportInfo{
				"t1": newTestTransportInfo("t1", 50*time.Millisecond, 20000, 5000),
			},
			state: nil,
			want:  "t1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SelectTransportMinRTT(tt.transports, tt.state)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSelectTransportMinRTT_StatsRecording(t *testing.T) {
	transports := map[transport.SubConnectionID]*TransportInfo{
		"fast": newTestTransportInfo("fast", 20*time.Millisecond, 20000, 5000),
		"slow": newTestTransportInfo("slow", 100*time.Millisecond, 20000, 5000),
	}
	state := NewMinRTTState()

	for range 5 {
		SelectTransportMinRTT(transports, state)
	}
	assert.Equal(t, uint64(5), state.TotalSelections.Load())

	state.SelectionCountsMu.Lock()
	assert.Equal(t, uint64(5), state.SelectionCounts["fast"])
	state.SelectionCountsMu.Unlock()
}
