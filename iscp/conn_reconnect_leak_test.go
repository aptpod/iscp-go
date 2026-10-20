package iscp_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	. "github.com/aptpod/iscp-go/v2/iscp"
	"github.com/aptpod/iscp-go/v2/message"
	"github.com/aptpod/iscp-go/v2/transport"
)

// gatedReconnectDialer implements transport.Dialer. Each Dial call creates and returns a new
// pipe-based dialer (newDialer), but only the gateAtN-th call notifies arrival by closing blocked
// and then blocks until proceed is closed. It is used to deterministically create the situation
// where reconnect is stuck in the middle of a dial (while holding wireConnMu).
type gatedReconnectDialer struct {
	gateAtN int32
	n       int32
	blocked chan struct{}
	proceed chan struct{}
	created chan *dialer
}

var _ transport.Dialer = (*gatedReconnectDialer)(nil)

func newGatedReconnectDialer(gateAtN int32) *gatedReconnectDialer {
	return &gatedReconnectDialer{
		gateAtN: gateAtN,
		blocked: make(chan struct{}),
		proceed: make(chan struct{}),
		created: make(chan *dialer, 8),
	}
}

// tryMockConnectRequest tries to respond to the handshake on the srv side.
// For a closed Conn, the handshake is cut off midway by the abort of the lifecycle ctx
// (protocolSessionConfig.Context), so read/write failures are ignored as the normal case
// (a version of mockConnectRequest without require).
func tryMockConnectRequest(srv *transport.MessageTransport) {
	if _, err := srv.ReadMessage(); err != nil {
		return
	}
	_ = srv.WriteMessage(&message.ConnectResponse{
		RequestID:       0,
		ProtocolVersion: "3.0.0",
		ResultCode:      message.ResultCodeSucceeded,
		ResultString:    "",
		ExtensionFields: &message.ConnectResponseExtensionFields{},
	})
}

func (g *gatedReconnectDialer) Dial(c transport.DialConfig) (transport.Transport, error) {
	n := atomic.AddInt32(&g.n, 1)
	d := newDialer(transport.NegotiationParams{})
	if n == g.gateAtN {
		close(g.blocked)
		<-g.proceed
	}
	g.created <- d
	return d, nil
}

// TestConn_Close_DuringReconnectDoesNotLeakEstablishedSession is a reproduction test that, when
// reconnect is closed during a dial, nobody leaks the session (its materials) obtained after the
// dial completes.
// It was originally written to reproduce a regression introduced when wireConnMu was changed to
// TryLock (a give-up close() cannot close the new session). The party responsible for cleanup has
// changed as the implementation evolved:
//   - Moving the dial outside the lock: the session is established after the dial completes,
//     and the closed re-check in reconnect (before assignment) calls res.Close()
//   - Propagating ctx to the dial: the closed lifecycle ctx aborts the handshake, so dialWire
//     itself closes the transport on its error path and the session is never established. The
//     closed re-check remains as a guard for the remaining race window where Close happens
//     between dialWire succeeding and the lock being acquired
//
// This test verifies that no leak occurs in the scenario "Close during the dial -> dial completes"
// without fixing which party performs the cleanup.
//
// Scenario:
//  1. Establish the first connection
//  2. Close the server side (d1.srv) to fire wireConn.Closed() and
//     trigger connLifecycle.reconnect()
//  3. reconnect() enters the second dial (outside the lock) and is deterministically
//     stopped there by gatedReconnectDialer
//  4. Meanwhile call Conn.Close(ctx). close() acquires the lock immediately, and the
//     Disconnect send to the already closed old wireConn (from d1) fails immediately and returns
//  5. Release the dial block. The dial succeeds, but the closed ctx aborts the
//     handshake and dialWire closes the transport (from d2)
//
// goleak.VerifyNone(t) determines whether any goroutine from d2 remains.
func TestConn_Close_DuringReconnectDoesNotLeakEstablishedSession(t *testing.T) {
	defer goleak.VerifyNone(t)

	g := newGatedReconnectDialer(2) // gate on the second Dial (reconnect)
	RegisterDialer(TransportTest, func() transport.Dialer { return g })

	// Server-side handling of the first connection. After returning ConnectResponse, close the
	// server side to cause EOF in the client-side readReliableLoop and fire
	// wireConn.Closed() to trigger reconnect.
	srv1Closed := make(chan struct{})
	go func() {
		defer close(srv1Closed)
		d1 := <-g.created
		mockConnectRequest(t, d1.srv)
		_ = d1.srv.Close()
	}()

	conn, err := Connect("dummy", TransportTest)
	require.NoError(t, err)
	<-srv1Closed

	// Wait until reconnect reaches the second dial (gated by gatedReconnectDialer)
	// and blocks there.
	select {
	case <-g.blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("reconnect did not reach the gated dial within timeout")
	}

	// Call Close while reconnect is blocked in the dial.
	closeDone := make(chan error, 1)
	go func() { closeDone <- conn.Close(context.Background()) }()

	select {
	case err := <-closeDone:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Conn.Close did not return while reconnect was blocked on dial")
	}

	// Release the dial block. The server side tries to respond, but the closed ctx aborts the
	// handshake, so failing midway is the normal case (tryMockConnectRequest ignores errors).
	// Whatever stage it reaches, the goleak at the end verifies that no goroutine from d2 remains.
	d2Done := make(chan struct{})
	go func() {
		defer close(d2Done)
		d2 := <-g.created
		tryMockConnectRequest(d2.srv)
	}()
	close(g.proceed)

	select {
	case <-d2Done:
	case <-time.After(5 * time.Second):
		t.Fatal("reconnect did not complete dialing the second (gated) transport")
	}

	// Grace period until connLifecycle.run() detects the error from reconnect() and exits.
	// goleak.VerifyNone has its own grace period with retries, so this is normally unnecessary,
	// but wait briefly to stabilize the timing at which subsequent goroutines (readReliableLoop etc.
	// if there is a bug) pile up.
	time.Sleep(100 * time.Millisecond)
}

// TestConn_Close_ReturnsWithoutWaitingForBlockedReconnectDial verifies that the fix for Close
// blocking indefinitely is structural rather than a "rescue by timeout".
//
// While reconnect is stuck in the middle of a dial (gatedReconnectDialer), call
// Close(context.Background()) and confirm it returns immediately without relying on the timeout
// (disconnectSendTimeout=3s). If the dial is done outside wireConnMu, Close can acquire the lock
// immediately, and the Disconnect send to the already closed old wireConn fails immediately and returns.
func TestConn_Close_ReturnsWithoutWaitingForBlockedReconnectDial(t *testing.T) {
	defer goleak.VerifyNone(t)

	g := newGatedReconnectDialer(2) // gate on the second Dial (reconnect)
	RegisterDialer(TransportTest, func() transport.Dialer { return g })

	srv1Closed := make(chan struct{})
	go func() {
		defer close(srv1Closed)
		d1 := <-g.created
		mockConnectRequest(t, d1.srv)
		_ = d1.srv.Close()
	}()

	conn, err := Connect("dummy", TransportTest)
	require.NoError(t, err)
	<-srv1Closed

	select {
	case <-g.blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("reconnect did not reach the gated dial within timeout")
	}

	// Call Close while reconnect is in the dial (stopped at the gate).
	start := time.Now()
	closeDone := make(chan error, 1)
	go func() { closeDone <- conn.Close(context.Background()) }()

	// Wait longer than disconnectSendTimeout (3s) before judging the elapsed time, to distinguish
	// "returned after being rescued by the timeout" (about 3s) from "returned without waiting"
	// (millisecond order). Do not use Fatal on failure so that cleanup (releasing the gate) still runs.
	select {
	case err := <-closeDone:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		assert.Fail(t, "Conn.Close did not return while reconnect was dialing")
	}
	assert.Less(t, time.Since(start), time.Second,
		"Close should return without waiting for any timeout while dial is in progress")

	// Release the dial block and wait for cleanup of the transport that should be discarded
	// (abort of the handshake by the closed ctx -> Close on the error path of dialWire).
	// The server-side response failing due to the abort is the normal case.
	d2Done := make(chan struct{})
	go func() {
		defer close(d2Done)
		d2 := <-g.created
		tryMockConnectRequest(d2.srv)
	}()
	close(g.proceed)

	select {
	case <-d2Done:
	case <-time.After(5 * time.Second):
		t.Fatal("reconnect did not complete dialing the second (gated) transport")
	}
	time.Sleep(100 * time.Millisecond)
}
