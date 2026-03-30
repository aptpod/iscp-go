package multi

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aptpod/iscp-go/v2/log"
	"github.com/aptpod/iscp-go/v2/transport"
	"github.com/aptpod/iscp-go/v2/transport/reconnect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testPipeTransport wraps transport.ReadWriter (from Pipe) to implement
// transport.Transport and transport.Closer interfaces required by reconnect.Dial.
type testPipeTransport struct {
	transport.ReadWriter
	negotiationParams transport.NegotiationParams
}

func (t *testPipeTransport) AsUnreliable() (transport.UnreliableTransport, bool) {
	return nil, false
}

func (t *testPipeTransport) NegotiationParams() transport.NegotiationParams {
	return t.negotiationParams
}

func (t *testPipeTransport) Name() transport.Name {
	return "test-pipe"
}

func (t *testPipeTransport) CloseWithStatus(_ transport.CloseStatus) error {
	return t.ReadWriter.Close()
}

// TestByteBalancedSelector_Get_BlocksWhenTransportReconnecting demonstrates that
// ByteBalancedSelector.Get() blocks when any reconnect.Transport is in
// reconnecting state.
//
// Root cause: selectMinBytes() calls reconnect.Transport.TxBytesCounterValue()
// which acquires r.mu.Lock(). When reconnect() is in progress, it holds r.mu.Lock()
// for the entire reconnection process (including retries and sleep intervals).
// This blocks TxBytesCounterValue() and consequently Get().
//
// Expected behavior (after fix): Get() should skip reconnecting transports
// and return quickly with a connected transport's ID.
func TestByteBalancedSelector_Get_BlocksWhenTransportReconnecting(t *testing.T) {
	// Create pipe pairs for initial connections
	pipeA1, pipeA2 := transport.Pipe()
	pipeB1, pipeB2 := transport.Pipe()

	// Channel to signal when reconnection dialer is called (r.mu.Lock is held at this point)
	reconnectStarted := make(chan struct{})
	var reconnectStartedOnce sync.Once

	// Channel to unblock the reconnection dialer
	blockReconnect := make(chan struct{})

	var dialCountA atomic.Int32
	var dialCountB atomic.Int32

	// Dialer A: always succeeds (we won't trigger reconnection on A)
	dialerA := transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
		dialCountA.Add(1)
		return &testPipeTransport{
			ReadWriter:        pipeA1,
			negotiationParams: dc.NegotiationParams(),
		}, nil
	})

	// Dialer B: first call succeeds, subsequent calls block indefinitely.
	// This simulates a transport stuck in reconnection where r.mu.Lock() is held.
	dialerB := transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
		n := dialCountB.Add(1)
		if n == 1 {
			return &testPipeTransport{
				ReadWriter:        pipeB1,
				negotiationParams: dc.NegotiationParams(),
			}, nil
		}
		// Signal that reconnection has started (r.mu.Lock is already held at this point)
		reconnectStartedOnce.Do(func() { close(reconnectStarted) })
		// Block until test cleanup
		<-blockReconnect
		return nil, fmt.Errorf("test: reconnect canceled")
	})

	// Create reconnect transports
	trA, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: dialerA,
		DialConfig: transport.DialConfig{
			SubConnectionID:   "trA",
			SuperConnectionID: "test-group",
		},
		MaxReconnectAttempts: 1,
		ReconnectInterval:    time.Millisecond,
		HeartbeatInterval:    time.Hour, // Don't send pings during test
		HeartbeatTimeout:     time.Hour, // Don't timeout during test
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)

	trB, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: dialerB,
		DialConfig: transport.DialConfig{
			SubConnectionID:   "trB",
			SuperConnectionID: "test-group",
		},
		MaxReconnectAttempts: 1,
		ReconnectInterval:    time.Millisecond,
		HeartbeatInterval:    time.Hour,
		HeartbeatTimeout:     time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)

	// Cleanup: must unblock dialer before closing transports to avoid deadlock
	t.Cleanup(func() {
		close(blockReconnect)
		trB.Close()
		trA.Close()
		pipeA2.Close()
	})

	// Wait for both transports to establish initial connections
	require.Eventually(t, func() bool {
		return trA.Status() == reconnect.StatusConnected
	}, 5*time.Second, 10*time.Millisecond, "trA should connect")
	require.Eventually(t, func() bool {
		return trB.Status() == reconnect.StatusConnected
	}, 5*time.Second, 10*time.Millisecond, "trB should connect")

	// Create multi.Transport directly (avoiding NewTransport goroutines for test simplicity)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mt := &Transport{
		ctx:    ctx,
		cancel: cancel,
		transportMap: map[transport.SubConnectionID]*reconnect.Transport{
			"trA": trA,
			"trB": trB,
		},
		logger: log.NewNop(),
	}

	// Create ByteBalancedSelector and set the multi transport
	selector := NewByteBalancedSelector([]transport.SubConnectionID{"trA", "trB"})
	selector.SetMultiTransport(mt)

	// Trigger reconnection on transport B by closing the remote end of its pipe.
	// This causes pipeB1.Read() to return EOF → reconnect.Transport's readLoop
	// calls reconnect() → acquires r.mu.Lock() → calls dialer which blocks.
	pipeB2.Close()

	// Wait for reconnection to actually start (dialer called with r.mu.Lock held)
	select {
	case <-reconnectStarted:
		// Good: reconnect dialer is now blocking, r.mu.Lock() is held
	case <-time.After(5 * time.Second):
		t.Fatal("reconnection did not start within timeout")
	}

	// Verify transport B is in reconnecting state
	require.Equal(t, reconnect.StatusReconnecting, trB.Status())

	// Now test: Get() should NOT block even though transport B is reconnecting.
	// Currently, Get() → selectMinBytes() → TxBytesCounterValue() → r.mu.Lock() → BLOCKS
	done := make(chan transport.SubConnectionID, 1)
	go func() {
		done <- selector.Get(context.Background(), 100)
	}()

	select {
	case id := <-done:
		// Expected behavior after fix: Get() returns quickly with connected transport
		assert.Equal(t, transport.SubConnectionID("trA"), id,
			"should select the connected transport, not the reconnecting one")
	case <-time.After(2 * time.Second):
		// Current buggy behavior: Get() blocks because TxBytesCounterValue()
		// is waiting for r.mu.Lock() held by reconnect()
		t.Fatal("ByteBalancedSelector.Get() blocked for >2s when a transport is reconnecting - " +
			"this is caused by selectMinBytes() calling TxBytesCounterValue() which acquires " +
			"r.mu.Lock() held by reconnect()")
	}
}
