package reconnect_test

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/aptpod/iscp-go/v2/log"
	"github.com/aptpod/iscp-go/v2/transport"
	. "github.com/aptpod/iscp-go/v2/transport/reconnect"
)

var errDialRefused = errors.New("test: dial refused")

// failAfterFirstDialer is a dialer that returns the mock only on the first dial and returns an error
// immediately afterwards. It simulates a situation where "reconnect attempts keep failing".
type failAfterFirstDialer struct {
	mock      transport.Transport
	dialCount atomic.Int32
}

func (d *failAfterFirstDialer) Dial(transport.DialConfig) (transport.Transport, error) {
	if d.dialCount.Add(1) == 1 {
		return d.mock, nil
	}
	return nil, errDialRefused
}

// alwaysFailDialer is a dialer that always returns an error immediately. It simulates a situation
// where "the initial connection keeps failing".
type alwaysFailDialer struct {
	dialCount atomic.Int32
}

func (d *alwaysFailDialer) Dial(transport.DialConfig) (transport.Transport, error) {
	d.dialCount.Add(1)
	return nil, errDialRefused
}

// TestReconnectTransport_ReconnectIntervalWaitInterruptedByClose verifies that the wait between attempts
// (reconnectInterval) in doReconnect is cut short by the cancellation of r.ctx.
//
// doReconnect waits between attempts while holding reconnectMu, so if this wait cannot
// be interrupted, CloseWithStatus (cancel -> waitForReconnectToFinish waits on
// reconnectMu) blocks for up to an extra reconnectInterval.
// reconnectInterval is an externally configured value with no upper bound, so the time Close
// takes grows in proportion to the configured value.
//
// Before the fix: Close does not return until the wait expires (5 seconds), and the 3-second detection times out.
func TestReconnectTransport_ReconnectIntervalWaitInterruptedByClose(t *testing.T) {
	defer goleak.VerifyNone(t)

	mock := newMockCountingTransport("mock1")
	d := &failAfterFirstDialer{mock: mock}

	tr, err := Dial(DialConfig{
		Dialer: d,
		DialConfig: transport.DialConfig{
			SubConnectionID: "sub1",
			TransportType:   transport.NegotiationNameWebSocket,
		},
		MaxReconnectAttempts: -1,
		ReconnectInterval:    5 * time.Second,
		HeartbeatInterval:    time.Hour,
		HeartbeatTimeout:     time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)

	waitForTransportConnected(t, tr)

	// Bring down the underlying transport to trigger a reconnect, and wait until the second dial
	// fails and enters the wait between attempts.
	mock.Close()
	require.Eventually(t,
		func() bool { return d.dialCount.Load() >= 2 },
		5*time.Second, 10*time.Millisecond,
		"reconnect should attempt a second dial and fail into the interval wait",
	)
	require.Eventually(t,
		func() bool { return tr.Status() == StatusReconnecting },
		5*time.Second, 10*time.Millisecond,
	)

	closeDone := make(chan error, 1)
	start := time.Now()
	go func() { closeDone <- tr.Close() }()

	select {
	case err := <-closeDone:
		require.NoError(t, err)
		assert.Less(t, time.Since(start), 2*time.Second,
			"Close should not wait out the full reconnectInterval")
	case <-time.After(3 * time.Second):
		assert.Fail(t, "Close() did not return within 3s: the inter-attempt wait is not interruptible")
		<-closeDone // cleanup (it returns once the wait expires)
	}
}

// TestReconnectTransport_InitialConnectIntervalWaitInterruptedByClose verifies that the wait between attempts
// (reconnectInterval) in initialConnect is cut short by the cancellation of r.ctx.
//
// CloseWithStatus does not wait for initialConnect to finish (it does not take reconnectMu), so Close itself
// returns immediately, but if the wait cannot be interrupted, the initialConnect goroutine remains
// for up to reconnectInterval after Close. goleak detects the remaining goroutine.
func TestReconnectTransport_InitialConnectIntervalWaitInterruptedByClose(t *testing.T) {
	defer goleak.VerifyNone(t)

	d := &alwaysFailDialer{}

	tr, err := Dial(DialConfig{
		Dialer: d,
		DialConfig: transport.DialConfig{
			SubConnectionID: "sub1",
			TransportType:   transport.NegotiationNameWebSocket,
		},
		MaxReconnectAttempts: -1,
		ReconnectInterval:    5 * time.Second,
		HeartbeatInterval:    time.Hour,
		HeartbeatTimeout:     time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)

	// Wait until the first dial fails and enters the wait between attempts, then Close.
	require.Eventually(t,
		func() bool { return d.dialCount.Load() >= 1 },
		5*time.Second, 10*time.Millisecond,
		"initial connect should attempt a dial and fail into the interval wait",
	)
	require.NoError(t, tr.Close())
}
