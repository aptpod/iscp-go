package iscp_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	. "github.com/aptpod/iscp-go/v2/iscp"
	"github.com/aptpod/iscp-go/v2/transport"
)

var errDialAfterClose = errors.New("test: dial rejected after close")

// blockingCloseTransport is a wrapper that gates Close of transport.Transport.
// Only the first Close call notifies entered and blocks until release;
// the second and later calls pass straight through to the lower layer.
//
// Why "the first Close = old.Close() of reconnect()" holds: this test handshakes with v4
// (mockConnectRequestV4), so keepAliveLoop does not start and there is no path that calls
// transport.Close() in the teardown of a server disconnect.
// readReliableLoop merely returns on a read error and does not call transport.Close() (it does so
// only on receiving a Disconnect message; this test only closes the server-side pipe, so that is
// not reached), and disconnect detection proceeds in the order: read error -> read loop
// returns -> wg.Wait in runWire -> defer c.cancel() -> Closed() fires.
// After that, old.Close() of reconnect() reaches this wrapper first.
// wireConn.Close() on the close() side comes after the gate has been consumed, so it is not blocked.
//
// Do not handshake with v3: keepAliveLoop would start, and c.Close() -> c.transport.Close() in its
// teardown could consume the gate before reconnect() does.
// keepAliveLoop calls sendPing() right after starting without waiting for the ticker, so with a
// mock like this test that closes the server-side pipe right after ConnectResponse, the Write of
// Ping fails immediately with an error (if it could be sent before closing, no Pong returns and it
// fails at the default pingTimeout of 1 second). Either path leads to c.Close(). Once the gate is
// consumed, old.Close() passes straight through and the test does not FAIL even if it is moved back
// inside the lock (the test is silently disabled).
type blockingCloseTransport struct {
	transport.Transport
	gated   atomic.Bool
	entered chan struct{}
	release chan struct{}
}

func (b *blockingCloseTransport) Close() error {
	if b.gated.CompareAndSwap(false, true) {
		close(b.entered)
		<-b.release
	}
	return b.Transport.Close()
}

// TestConn_Close_ReturnsWithoutWaitingForBlockedOldSessionClose is a regression test for moving
// the close of the old session on reconnect out of wireConnMu.
//
// If the first section of reconnect() performs old.Close() inside the lock, then with an
// implementation where Close of the lower-layer transport blocks (e.g. CloseWithStatus of
// reconnect.Transport does not return until the running dial completes), Conn.Close() gets dragged
// into waiting for the wireConnMu lock. This test plugs in a transport whose Close blocks and
// verifies that Conn.Close() returns without waiting even while old.Close() is blocked.
//
// Why Conn.Close is used as the oracle: public send APIs such as OpenUpstream / SendMetadata all
// pass through the state gate of c.send (WaitUntilOrClosed, ctx-aware), and during a reconnect
// (connStatusReconnecting) they leave via ctx before reaching wireConnMu, so they cannot tell
// whether the lock is held. During a reconnect, it is close() itself that goes for wireConnMu
// directly, and the property being protected is that Conn.Close() is not dragged into waiting for
// the wireConnMu lock.
//
// Note: this test does not guarantee an improvement in the time Close takes. Close returns quickly
// here only because of the test setup in which the gate blocks just the first call (old.Close() of
// reconnect). In the real world, the reason old.Close() blocks (Close of the lower-layer transport
// waits for the running dial to complete) applies equally to c.wireConn.Close() called by
// Conn.Close() itself (the same object as old, since the assignment of the second section has not
// happened yet), so the time Close takes is almost unchanged.
// Bounding the lower-layer Close is a remaining issue, to be resolved by propagating ctx to the dial.
func TestConn_Close_ReturnsWithoutWaitingForBlockedOldSessionClose(t *testing.T) {
	defer goleak.VerifyNone(t)

	bct := &blockingCloseTransport{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	created := make(chan *dialer, 8)
	var dialCount atomic.Int32
	RegisterDialer(TransportTest, func() transport.Dialer {
		return transport.DialerFunc(func(transport.DialConfig) (transport.Transport, error) {
			if dialCount.Add(1) > 1 {
				// Due to a race between Close and the lifecycle ctx cancellation (asynchronous via the state
				// watcher goroutine), reconnect may proceed to one last dial after the gate is released.
				// Returning a real transport would make a handshake with no response block until the watchdog
				// (30 seconds) and be detected by goleak, so abort it immediately with an error.
				return nil, errDialAfterClose
			}
			d := newDialer(transport.NegotiationParams{})
			created <- d
			bct.Transport = d
			return bct, nil
		})
	})

	// After answering the first connection, close the server side to trigger reconnect.
	// The first section of reconnect reaches old.Close() -> bct.Close() and blocks.
	// See the comment on blockingCloseTransport for why the handshake uses v4.
	srv1Done := make(chan struct{})
	go func() {
		defer close(srv1Done)
		d1 := <-created
		mockConnectRequestV4(t, d1.srv)
		_ = d1.srv.Close()
	}()

	conn, err := Connect("dummy", TransportTest)
	require.NoError(t, err)
	<-srv1Done

	select {
	case <-bct.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("reconnect did not reach old.Close() within timeout")
	}

	// Call Conn.Close() while old.Close() is blocked. If the move out of the lock is effective,
	// the lock is free and Close returns without waiting.
	start := time.Now()
	closeDone := make(chan error, 1)
	go func() { closeDone <- conn.Close(context.Background()) }()

	closed := false
	select {
	case err := <-closeDone:
		closed = true
		assert.NoError(t, err)
		assert.Less(t, time.Since(start), 2*time.Second,
			"Close should not wait for the blocked old.Close()")
	case <-time.After(5 * time.Second):
		assert.Fail(t, "Conn.Close did not return while old session Close was blocked")
	}

	// Cleanup: release the gate. The lifecycle ctx has already been cancelled, so reconnect
	// does not proceed to the dial and exits.
	close(bct.release)
	if !closed {
		select {
		case <-closeDone:
		case <-time.After(5 * time.Second):
			t.Fatal("Close never returned even after releasing the gate")
		}
	}
	// Grace period for the lifecycle goroutine to exit (same as the existing tests).
	time.Sleep(100 * time.Millisecond)
}
