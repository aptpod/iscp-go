package multi

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeClock is a monotonically increasing clock for tests.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Unix(0, 0)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func TestNoConnectedTracker_AlwaysFalseWhenThresholdIsNonPositive(t *testing.T) {
	for _, timeout := range []time.Duration{0, -time.Second} {
		clock := newFakeClock()
		tr := newNoConnectedTracker(timeout)
		tr.now = clock.Now

		require.False(t, tr.observe(false))
		clock.Advance(24 * time.Hour)
		require.False(t, tr.observe(false), "it is disabled, so it never gives up no matter how many hours pass")
	}
}

func TestNoConnectedTracker_ReturnsTrueWhenThresholdExceeded(t *testing.T) {
	clock := newFakeClock()
	tr := newNoConnectedTracker(100 * time.Millisecond)
	tr.now = clock.Now

	// The first observation only records.
	require.False(t, tr.observe(false))

	clock.Advance(99 * time.Millisecond)
	require.False(t, tr.observe(false), "do not give up yet below the threshold")

	clock.Advance(1 * time.Millisecond)
	require.True(t, tr.observe(false), "give up exactly at the threshold")
}

func TestNoConnectedTracker_ResetsMeasurementWhenConnectionReturns(t *testing.T) {
	clock := newFakeClock()
	tr := newNoConnectedTracker(100 * time.Millisecond)
	tr.now = clock.Now

	require.False(t, tr.observe(false))
	clock.Advance(90 * time.Millisecond)

	// Reset as soon as even one transport becomes Connected.
	require.False(t, tr.observe(true))

	clock.Advance(90 * time.Millisecond)
	require.False(t, tr.observe(false), "the first observation after the reset only records")

	clock.Advance(99 * time.Millisecond)
	require.False(t, tr.observe(false), "after the reset, wait for the threshold again")

	clock.Advance(1 * time.Millisecond)
	require.True(t, tr.observe(false))
}

// It is level-triggered: repeated observe(false) calls with no state change
// are enough to detect the threshold being exceeded (no transition event is needed).
func TestNoConnectedTracker_DetectsWithoutTransition(t *testing.T) {
	clock := newFakeClock()
	tr := newNoConnectedTracker(50 * time.Millisecond)
	tr.now = clock.Now

	require.False(t, tr.observe(false))
	for i := 0; i < 4; i++ {
		clock.Advance(10 * time.Millisecond)
		require.False(t, tr.observe(false))
	}
	clock.Advance(10 * time.Millisecond)
	require.True(t, tr.observe(false))
}

// Keeps returning true when observation continues after the threshold is exceeded (assumes the caller prevents duplicate teardown).
func TestNoConnectedTracker_KeepsReturningTrueAfterExceeded(t *testing.T) {
	clock := newFakeClock()
	tr := newNoConnectedTracker(10 * time.Millisecond)
	tr.now = clock.Now

	require.False(t, tr.observe(false))
	clock.Advance(10 * time.Millisecond)
	require.True(t, tr.observe(false))
	require.True(t, tr.observe(false))
}

func TestNoConnectedTracker_DoesNotBreakUnderConcurrentCalls(t *testing.T) {
	tr := newNoConnectedTracker(time.Hour)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				tr.observe(j%2 == 0)
			}
		}(i)
	}
	wg.Wait()
	// The goal is to have -race detect problems. Here we only check that it does not panic.
}
