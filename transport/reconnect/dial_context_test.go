package reconnect_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aptpod/iscp-go/v2/log"
	"github.com/aptpod/iscp-go/v2/transport"
	. "github.com/aptpod/iscp-go/v2/transport/reconnect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// ctxAwareBlockingDialer is a transport.ContextDialer implementation that returns the mock on the
// first dial and blocks until ctx.Done() from the second dial on.
// It simulates a dialer that "hangs on dial but respects ctx".
type ctxAwareBlockingDialer struct {
	mock      transport.Transport
	dialCount atomic.Int32
}

func (d *ctxAwareBlockingDialer) Dial(dc transport.DialConfig) (transport.Transport, error) {
	return d.DialContext(context.Background(), dc)
}

func (d *ctxAwareBlockingDialer) DialContext(ctx context.Context, _ transport.DialConfig) (transport.Transport, error) {
	if d.dialCount.Add(1) == 1 {
		return d.mock, nil
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestReconnectTransport_CloseDuringReconnect_ContextDialerAbortsAndReturns verifies that, even if the dial
// for a reconnect is blocked, as long as the dialer implements ContextDialer, cancel() in
// CloseWithStatus interrupts the dial and Close() returns in finite time.
//
// This is the resolved version of the issue that, with a legacy Dialer, Close cannot return until the dial is released,
// which TestReconnectTransport_CloseDuringReconnect (concurrent_close_test.go) records.
// The fallback behavior of a legacy Dialer continues to be recorded by that test.
// It also verifies with goleak that no interrupted dial goroutine remains.
func TestReconnectTransport_CloseDuringReconnect_ContextDialerAbortsAndReturns(t *testing.T) {
	defer goleak.VerifyNone(t)

	mock := newMockCountingTransport("mock1")
	d := &ctxAwareBlockingDialer{mock: mock}

	tr, err := Dial(DialConfig{
		Dialer: d,
		DialConfig: transport.DialConfig{
			SubConnectionID: "sub1",
			TransportType:   transport.NegotiationNameWebSocket,
		},
		MaxReconnectAttempts: -1,
		ReconnectInterval:    10 * time.Millisecond,
		HeartbeatInterval:    time.Hour,
		HeartbeatTimeout:     time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)

	waitForTransportConnected(t, tr)

	// Bring down the underlying transport to trigger a reconnect, and wait until it
	// enters the second dial (blocked waiting on ctx).
	mock.Close()
	require.Eventually(t,
		func() bool { return d.dialCount.Load() >= 2 },
		5*time.Second, 10*time.Millisecond,
		"dialer should be invoked a second time and block on ctx",
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
		assert.Less(t, time.Since(start), time.Second,
			"Close should return promptly by canceling the in-flight dial")
	case <-time.After(3 * time.Second):
		assert.Fail(t, "Close() did not return within 3s while dial is blocked (dial ctx not honored)")
	}
}
