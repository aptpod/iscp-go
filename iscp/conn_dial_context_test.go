package iscp_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	. "github.com/aptpod/iscp-go/v2/iscp"
	"github.com/aptpod/iscp-go/v2/transport"
)

// ctxGatedReconnectDialer implements transport.ContextDialer. The first dial
// returns a pipe-based dialer, and subsequent dials block until ctx.Done().
// It is the ctx-aware version of gatedReconnectDialer (conn_reconnect_leak_test.go) and
// simulates a dialer that hangs but respects ctx.
type ctxGatedReconnectDialer struct {
	n           int32
	blockedOnce sync.Once
	blocked     chan struct{}
	created     chan *dialer
}

var (
	_ transport.Dialer        = (*ctxGatedReconnectDialer)(nil)
	_ transport.ContextDialer = (*ctxGatedReconnectDialer)(nil)
)

func newCtxGatedReconnectDialer() *ctxGatedReconnectDialer {
	return &ctxGatedReconnectDialer{
		blocked: make(chan struct{}),
		created: make(chan *dialer, 8),
	}
}

func (g *ctxGatedReconnectDialer) Dial(c transport.DialConfig) (transport.Transport, error) {
	return g.DialContext(context.Background(), c)
}

func (g *ctxGatedReconnectDialer) DialContext(ctx context.Context, _ transport.DialConfig) (transport.Transport, error) {
	if atomic.AddInt32(&g.n, 1) == 1 {
		d := newDialer(transport.NegotiationParams{})
		g.created <- d
		return d, nil
	}
	g.blockedOnce.Do(func() { close(g.blocked) })
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestConn_Close_ContextDialerAbortsAndReleasesReconnectDial verifies that, even when the
// reconnect dial is blocked, cancelling the lifecycle ctx via Close propagates to the dial
// through dialWire and the dial goroutine exits on its own.
//
// The test TestConn_Close_ReturnsWithoutWaitingForBlockedReconnectDial verifies that
// Close does not wait for the dial, but the blocked dial goroutine itself remained until
// the test released the gate. This test never releases the gate and uses goleak to verify
// that Close alone releases the dial goroutine. This is the core effect of propagating ctx to the dial.
func TestConn_Close_ContextDialerAbortsAndReleasesReconnectDial(t *testing.T) {
	defer goleak.VerifyNone(t)

	g := newCtxGatedReconnectDialer()
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

	// Wait until the reconnect dial starts waiting on ctx.
	select {
	case <-g.blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("reconnect did not reach the blocking dial within timeout")
	}

	start := time.Now()
	require.NoError(t, conn.Close(context.Background()))
	assert.Less(t, time.Since(start), time.Second)

	// Do not release the gate. If Close's cancel propagates to the dial's ctx,
	// the dial goroutine exits on its own and goleak.VerifyNone passes.
}

// TestConn_Close_AbortsReconnectHandshakeWait verifies that, even when the reconnect dial
// succeeded but the server does not return a ConnectResponse (no handshake response),
// cancelling the lifecycle ctx via Close aborts the handshake wait and releases the reconnect
// goroutine without waiting for the watchdog (connectHandshakeTimeout = 30 seconds).
// This verifies the abort path of protocolSessionConfig.Context
// (context.AfterFunc -> transport.Close).
func TestConn_Close_AbortsReconnectHandshakeWait(t *testing.T) {
	defer goleak.VerifyNone(t)

	created := make(chan *dialer, 8)
	RegisterDialer(TransportTest, func() transport.Dialer {
		return transport.DialerFunc(func(transport.DialConfig) (transport.Transport, error) {
			d := newDialer(transport.NegotiationParams{})
			created <- d
			return d, nil
		})
	})

	// Close the server side right after answering the first connection to trigger a reconnect.
	go func() {
		d1 := <-created
		mockConnectRequest(t, d1.srv)
		_ = d1.srv.Close()
	}()

	conn, err := Connect("dummy", TransportTest)
	require.NoError(t, err)

	// Wait for the reconnect dial to complete (creation of the second dialer). After that,
	// reconnect waits for the handshake (waitForConnected), but no ConnectResponse
	// is returned.
	var d2 *dialer
	select {
	case d2 = <-created:
	case <-time.After(5 * time.Second):
		t.Fatal("reconnect did not dial within timeout")
	}
	t.Cleanup(func() { _ = d2.srv.Close() })

	// Grace period until reconnect enters waitForConnected.
	time.Sleep(50 * time.Millisecond)

	// Close during the handshake wait. Cancelling the lifecycle ctx closes the transport
	// via protocolSessionConfig.Context, and the reconnect goroutine must exit without
	// waiting for the 30-second watchdog (goleak detects it otherwise).
	start := time.Now()
	require.NoError(t, conn.Close(context.Background()))
	assert.Less(t, time.Since(start), time.Second)
}
