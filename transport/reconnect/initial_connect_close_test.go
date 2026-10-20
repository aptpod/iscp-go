package reconnect_test

import (
	"testing"
	"time"

	"github.com/aptpod/iscp-go/v2/log"
	"github.com/aptpod/iscp-go/v2/transport"
	. "github.com/aptpod/iscp-go/v2/transport/reconnect"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// TestReconnectTransport_CloseDuringInitialConnectClosesDialedTransport is a regression test
// that verifies the underlying transport does not leak due to a race between
// initialConnect and CloseWithStatus.
//
// Race path (the bug before the fix):
//
//  1. initialConnect checks closed only before dialer.Dial
//  2. If CloseWithStatus completes after passing the check (while dialer.Dial is running),
//     it sees r.transport == nil, closes nothing, and Close completes
//     (waitForReconnectToFinish waits only on reconnectMu, and initialConnect does not
//     take reconnectMu, so it slips through the synchronization)
//  3. Afterwards initialConnect sets r.transport and returns Status to Connected,
//     but nobody is left to close this transport -> permanent leak
//
// It was actually observed as a goleak flaky test in the transport/multi package
// (in give_up_defaults_test.go, TestCalcNoConnectedTransportTimeout_MatchesReconnectDefaults
// closes right after Dial without waiting for the connection to complete; only under high load
// when all packages run in parallel did it hit this window, and readLoop + the internal reader
// goroutine remained forever).
//
// This test makes the dialer "block until Close completes", so it hits
// this window 100% deterministically without relying on probability.
func TestReconnectTransport_CloseDuringInitialConnectClosesDialedTransport(t *testing.T) {
	defer goleak.VerifyNone(t)

	mock := newMockCountingTransport("mock1")
	dialEntered := make(chan struct{})
	closeDone := make(chan struct{})
	tr, err := Dial(DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			// Notify the test body that initialConnect passed the closed check and entered the dial,
			// then block until Close completes.
			// (If Close happens before reaching the dial, initialConnect is cut off by the closed
			// check at the top of the loop and never hits the race window, so this synchronization is needed)
			close(dialEntered)
			<-closeDone
			return mock, nil
		}),
		DialConfig: transport.DialConfig{
			SubConnectionID: "sub1",
		},
		MaxReconnectAttempts: 1,
		ReconnectInterval:    10 * time.Millisecond,
		HeartbeatInterval:    time.Hour,
		HeartbeatTimeout:     time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)

	// Wait until initialConnect enters dialer.Dial (= has passed the closed check).
	select {
	case <-dialEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("initialConnect did not reach dialer.Dial within 5s")
	}

	// initialConnect is blocked inside the dialer. This Close sees r.transport == nil
	// and completes with "nothing to close".
	require.NoError(t, tr.Close())

	// Release the dialer after Close completes. initialConnect will then receive the transport,
	// but it must detect that it is already closed and close it itself.
	close(closeDone)

	require.Eventually(t,
		func() bool { return mock.CloseCount() >= 1 },
		time.Second, 5*time.Millisecond,
		"the underlying transport dialed by initialConnect after Close completed should be closed")
	require.Equal(t, StatusDisconnected, tr.Status(),
		"Status must not return to Connected after Close")
}
