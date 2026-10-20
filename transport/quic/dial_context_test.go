package quic_test

import (
	"context"
	"strings"
	"testing"
	"time"

	quicgo "github.com/quic-go/quic-go"
	"github.com/stretchr/testify/require"

	"github.com/aptpod/iscp-go/v2/internal/testdata"
	"github.com/aptpod/iscp-go/v2/transport"
	. "github.com/aptpod/iscp-go/v2/transport/quic"
)

// TestDialer_DialContext_NegotiateWriteCanBeInterruptedByContext verifies that,
// even if negotiate after the QUIC handshake (writing params to the uni stream)
// blocks because flow control is exhausted, DialContext returns when ctx is canceled.
//
// Setup that makes stream.Write actually block:
//   - quic-go copies into the frame buffer and returns immediately when the
//     remaining write size is at most MaxPacketBufferSize (1452 bytes), so Write
//     does not block with the default parameter size (a few hundred bytes).
//     SuperConnectionID is part of a public API (transport.DialConfig) with no
//     length validation, so make it larger than both 1452 bytes and the flow
//     control window to enter the blocking branch.
//   - The server only accepts the connection and never accepts (reads) the uni
//     stream, so the credit does not grow beyond the initial window (limited to 4KiB).
func TestDialer_DialContext_NegotiateWriteCanBeInterruptedByContext(t *testing.T) {
	tlsConfig := testdata.GetTLSConfig()
	tlsConfig.NextProtos = []string{"iscp"}
	lis, err := quicgo.ListenAddr("localhost:0", tlsConfig, &quicgo.Config{
		InitialStreamReceiveWindow:     4096,
		MaxStreamReceiveWindow:         4096,
		InitialConnectionReceiveWindow: 4096,
		MaxConnectionReceiveWindow:     4096,
	})
	require.NoError(t, err)
	defer lis.Close()

	// The server only signals that it accepted the connection; it does not read the stream.
	accepted := make(chan struct{})
	srvCtx, srvCancel := context.WithCancel(context.Background())
	defer srvCancel()
	srvDone := make(chan struct{})
	go func() {
		defer close(srvDone)
		sess, err := lis.Accept(srvCtx)
		if err != nil {
			return
		}
		defer sess.CloseWithError(0, "test done")
		close(accepted)
		<-srvCtx.Done()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d := NewDialer(DialerConfig{TLSConfig: testdata.GetTLSConfig()})
	done := make(chan error, 1)
	go func() {
		_, err := d.DialContext(ctx, transport.DialConfig{
			Address:           lis.Addr().String(),
			SuperConnectionID: transport.SuperConnectionID(strings.Repeat("x", 256*1024)),
		})
		done <- err
	}()

	// Cancel after the handshake completes. The 100ms grace is a best-effort wait
	// for negotiate's Write to enter the blocked state (even if the cancel arrives
	// before Write, the deadline already set by AfterFunc makes Write fail
	// immediately, so what is verified does not change).
	select {
	case <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not accept the connection")
	}
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		require.Error(t, err)
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(3 * time.Second):
		t.Fatal("DialContext did not return after ctx cancellation: negotiate write is not ctx-aware")
	}
}
