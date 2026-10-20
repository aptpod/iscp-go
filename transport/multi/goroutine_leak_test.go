package multi_test

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aptpod/iscp-go/v2/log"
	"github.com/aptpod/iscp-go/v2/transport"
	. "github.com/aptpod/iscp-go/v2/transport/multi"
	"github.com/aptpod/iscp-go/v2/transport/reconnect"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// newExhaustingAfterConnectTransport creates a reconnect.Transport that "succeeds only on the first connection and always fails on every later reconnect".
// It exhausts the retries of doReconnect so that the transport falls to StatusDisconnected.
//
// **Do not fail from the first connection.** The exhaustion path of initialConnect calls
// r.cancel() inside doneProcess (transport/reconnect/transport.go:241-250, :253-256), so the goroutines are
// reclaimed naturally and the leak cannot be reproduced. The leak only occurs on the path where
// the retries of doReconnect are exhausted after the transport has been Connected once (:775-780).
//
// Measured on 2026-07-31: this was first written as a version that "always fails from the first connection", but that
// went through the exhaustion path of initialConnect (r.cancel() is called in doneProcess), so the goroutines were
// reclaimed naturally and the test PASSED without reproducing the leak. Only after switching to
// this version (succeeds only the first time) was it confirmed to FAIL.
//
// When transportType is an empty string, the v4 protocol feature is disabled and heartbeatLoop is not started
// (transport/reconnect/transport.go:199,227-229). Nothing leaks without v4, so
// the case that actually detects the leak is the one with v4 enabled. Both are written to prevent regressions.
func newExhaustingAfterConnectTransport(t *testing.T, mock *mockTransport, subConnID string, transportType transport.Name) *reconnect.Transport {
	t.Helper()
	var dialCount atomic.Int32
	rt, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			if dialCount.Add(1) == 1 {
				return mock, nil // succeeds only the first time
			}
			return nil, errors.New("test: reconnect always fails")
		}),
		DialConfig: transport.DialConfig{
			SubConnectionID:   transport.SubConnectionID(subConnID),
			SuperConnectionID: transport.SuperConnectionID(testSuperConnectionID),
			TransportType:     transportType,
		},
		MaxReconnectAttempts: 1, // exhausted after 1 reconnect
		ReconnectInterval:    10 * time.Millisecond,
		HeartbeatInterval:    time.Hour,
		HeartbeatTimeout:     time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)
	return rt
}

// TestMultiTransport_NoSubGoroutineLeakAfterAllSubsExhausted verifies that after all sub-connections
// have used up their finite retries and reached StatusDisconnected, multi.Transport
// closes the subs and reclaims the goroutines.
//
// Before the fix, transport.go:330-333 only called m.cancel(). The ctx of reconnect.Transport
// derives from context.Background() and has no parent-child relation with m.ctx (reconnect/transport.go:224),
// so the cancel was not propagated to the subs, and with v4 enabled heartbeatLoop remained (doReconnect
// returns by itself when its retries are exhausted, so dial does not continue and readLoop does not remain either).
func TestMultiTransport_NoSubGoroutineLeakAfterAllSubsExhausted(t *testing.T) {
	for _, tt := range []struct {
		name          string
		transportType transport.Name
	}{
		{name: "v4 disabled", transportType: ""},
		{name: "v4 enabled", transportType: transport.NegotiationNameWebSocket},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// Clean up after the check. t.Cleanup runs after all defers in the function
			// have run, so this order is the only way to verify "is it reclaimed without an explicit Close"
			// (closing with defer would hide the leak and make the test meaningless).
			defer goleak.VerifyNone(t)

			mock1 := newMockTransport("mock1")
			mock2 := newMockTransport("mock2")
			rt1 := newExhaustingAfterConnectTransport(t, mock1, "sub1", tt.transportType)
			rt2 := newExhaustingAfterConnectTransport(t, mock2, "sub2", tt.transportType)

			// Make it Connected once and then disconnect it (otherwise it goes through the initialConnect path
			// and the leak cannot be reproduced. See the helper comment).
			waitForConnected(t, rt1)
			waitForConnected(t, rt2)

			id1 := transport.SubConnectionID("transport1")
			id2 := transport.SubConnectionID("transport2")

			mt, err := NewTransport(TransportConfig{
				TransportMap: map[transport.SubConnectionID]*reconnect.Transport{
					id1: rt1,
					id2: rt2,
				},
				TransportSelector: newFixedTransportSelector(id1),
				Logger:            log.NewNop(),
				// Do not give up by the parent's threshold. Reach
				// MultiOverallStatusDisconnected only through the sub's own retry exhaustion, taking the path of the leak.
				NoConnectedTransportTimeout: 0,
			})
			require.NoError(t, err)
			t.Cleanup(func() { closeMultiAndWait(mt) })

			// Disconnect after registering with multi. Later reconnects always fail, so
			// MaxReconnectAttempts=1 is exhausted and the sub falls to StatusDisconnected.
			mock1.Close()
			mock2.Close()

			require.Eventually(t, func() bool {
				return mt.OverallStatus() == MultiOverallStatusDisconnected
			}, 3*time.Second, 10*time.Millisecond,
				"OverallStatus should become Disconnected once all subs exhaust their retries")

			// The teardown runs in a goroutine separate from the status callback, so wait a little.
			// Do not call an explicit Close (doing so would not verify the leak).
			time.Sleep(300 * time.Millisecond)
		})
	}
}
