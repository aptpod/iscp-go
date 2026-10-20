package iscp_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	. "github.com/aptpod/iscp-go/v2/iscp"
	"github.com/aptpod/iscp-go/v2/message"
	"github.com/aptpod/iscp-go/v2/transport"
)

// TestConn_ConcurrentClose verifies that calling Conn.Close(ctx) simultaneously from
// stressGoroutines goroutines makes all of them return in finite time without panicking.
//
// close guards only saveAndClearAll* with state.Swap, while SendDisconnect / wireConn.Close()
// are merely serialized by wireConnMu and run every time.
// Repetition confirms that the second and later calls also return safely.
func TestConn_ConcurrentClose(t *testing.T) {
	defer goleak.VerifyNone(t)
	d := newDialer(transport.NegotiationParams{})
	RegisterDialer(TransportTest, func() transport.Dialer { return d })

	srvDone := make(chan struct{})
	go func() {
		defer close(srvDone)
		mockConnectRequest(t, d.srv)
		// From here on, discard Disconnect (it may arrive multiple times). The server writes nothing afterwards.
		for {
			if _, err := d.srv.ReadMessage(); err != nil {
				return
			}
		}
	}()

	conn, err := Connect("dummy", TransportTest)
	require.NoError(t, err)

	var wg sync.WaitGroup
	for i := 0; i < stressGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			assert.NotPanics(t, func() {
				_ = conn.Close(context.Background()) // Only require no panic. Whether an error is returned does not matter.
			})
		}()
	}

	allDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(allDone)
	}()

	select {
	case <-allDone:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent Conn.Close did not return within timeout")
	}
	<-srvDone
}

// TestConn_Close_TimesOutWhenDisconnectSendBlocks_Repeated repeats the existing
// TestConn_Close_TimesOutWhenDisconnectSendBlocks (conn_test.go) stressIterationsSlow times.
// It waits for disconnectSendTimeout (3s) every time and is thus wall-clock dependent; using the
// iteration count of other packages (about 50 with the stress build) as is would simply inflate
// the wait time. Therefore the dedicated stressIterationsSlow is used
// (see stress_params_*.go).
func TestConn_Close_TimesOutWhenDisconnectSendBlocks_Repeated(t *testing.T) {
	for i := 0; i < stressIterationsSlow; i++ {
		func() {
			defer goleak.VerifyNone(t)
			d := newDialer(transport.NegotiationParams{})
			RegisterDialer(TransportTest, func() transport.Dialer { return d })

			go func() {
				mockConnectRequest(t, d.srv)
				// Never Read from now on. The Write that sends Disconnect keeps blocking in the
				// lower-layer pipe until the peer reads.
			}()

			conn, err := Connect("dummy", TransportTest)
			require.NoError(t, err)

			done := make(chan error, 1)
			go func() { done <- conn.Close(context.Background()) }()

			select {
			case err := <-done:
				assert.NoError(t, err, "iteration %d: the result of wireConn.Close() should be returned as is", i)
			case <-time.After(5 * time.Second):
				t.Fatalf("iteration %d: Conn.Close did not return within disconnectSendTimeout + margin", i)
			}
		}()
	}
}

// writeSignalReadWriter wraps transport.ReadWriter and sends a signal without blocking each
// time Write is called (= a message to send is about to be passed to the lower-layer pipe).
// The pipe uses a rendezvous over an unbuffered channel, so a Write call blocks until the peer
// Reads. The signal is sent right before the Write call, so the receiver can deterministically
// detect that Write was called (regardless of whether it blocks afterwards).
//
// Always replace d.ReadWriter before Connect (= before runWire/readReliableLoop starts).
// Replacing it after startup causes a data race between the read of d.ReadWriter held by
// readReliableLoop and the field assignment (detected with -race). Instead of consolidating the
// replacement before Connect, the signal can be sent multiple times rather than one-shot, and
// the caller counts which Write it is and receives only the required number of times.
type writeSignalReadWriter struct {
	transport.ReadWriter
	signal chan struct{}
}

func newWriteSignalReadWriter(rw transport.ReadWriter) *writeSignalReadWriter {
	return &writeSignalReadWriter{ReadWriter: rw, signal: make(chan struct{}, 8)}
}

func (w *writeSignalReadWriter) Write(b []byte) error {
	select {
	case w.signal <- struct{}{}:
	default:
	}
	return w.ReadWriter.Write(b)
}

// TestConn_OpenUpstream_CloseTimesOutWhileWriteBlocked verifies that, even if Conn.Close is
// called while OpenUpstream is blocked in the Write of SendUpstreamOpenRequest, Close returns
// within disconnectSendTimeout (3s) plus a margin.
//
// OpenUpstream does a round trip outside the lock on the snapshotted session, so wireConnMu is
// not held even if Write blocks. Close acquires the lock immediately and sends Disconnect, but
// since the peer does not Read on the same pipe, the Write of Disconnect also blocks, and the
// select gives up at disconnectSendTimeout and calls wireConn.Close(). This Close is called
// concurrently with the Write that is blocked on the OpenUpstream side; its safety has been
// confirmed individually for the Write/Close concurrency of each lower-layer transport
// (websocket-gorilla/coder, quic, webtransport)
// (e.g. gorilla/websocket documents "The Close ... method can be called concurrently
// with all other methods."; coder/websocket is designed so that Write internally watches a
// closed channel and is released by Close; quic-go's SendStream.Write is mutex-protected with
// no data race).
//
// As a result of wireConn.Close(), the Write blocked on the OpenUpstream side is released by the
// Close of the lower-layer pipe and returns an error, so SendUpstreamOpenRequest and in turn
// OpenUpstream itself finish with an error.
//
// Trying to create the state "OpenUpstream holds wireConnMu" with time.Sleep has a race where the
// Close side acquires wireConnMu before the goroutine on the OpenUpstream side reaches the Write
// call (rarely, especially under -race or high system load), which makes it flaky (extending the
// sleep 50ms -> 300ms -> 1s did not resolve it). Therefore d.ReadWriter is replaced with a
// wrapper that signals on Write, and Close is called after deterministically waiting for the
// Write call of OpenUpstream.
//
// Use protocol version 4.0.0 (mockConnectRequestV4). With 3.0.0, needsPingPong() is true and
// keepAliveLoop sends a Ping right after Connect (without waiting for the ticker), so the signal
// of writeSignalReadWriter fires erroneously on the Write of Ping instead of the Write of
// OpenUpstream (this actually happened and was the cause of a flaky test where Close returned
// before wireConnMu was released).
func TestConn_OpenUpstream_CloseTimesOutWhileWriteBlocked(t *testing.T) {
	defer goleak.VerifyNone(t)
	d := newDialer(transport.NegotiationParams{})
	// Replace it before readReliableLoop (started by Connect with go) reads d.ReadWriter.
	// The first signal corresponds to the Write of the ConnectRequest sent by Connect itself,
	// and the second is the SendUpstreamOpenRequest of OpenUpstream.
	sigRW := newWriteSignalReadWriter(d.ReadWriter)
	d.ReadWriter = sigRW
	RegisterDialer(TransportTest, func() transport.Dialer { return d })

	srvDone := make(chan struct{})
	go func() {
		defer close(srvDone)
		mockConnectRequestV4(t, d.srv)
		// The Close of the lower-layer pipe by wireConn.Close() unblocks the Write side of
		// UpstreamOpenRequest, so it fails before reaching the peer and is never sent.
		// From here on, discard everything until a Read error (the pipe was closed).
		for {
			if _, err := d.srv.ReadMessage(); err != nil {
				return
			}
		}
	}()

	conn, err := Connect("dummy", TransportTest)
	require.NoError(t, err)
	<-sigRW.signal // Consume the signal for the Write of ConnectRequest.

	openDone := make(chan struct{})
	go func() {
		defer close(openDone)
		_, _ = conn.OpenUpstream(context.Background(), "session_id")
	}()

	// Deterministically wait until OpenUpstream calls the Write of SendUpstreamOpenRequest (the
	// server does not Read, so the Write keeps blocking on the pipe).
	// This replaces synchronization that relies on scheduling via time.Sleep and removes the cause of flakiness.
	<-sigRW.signal

	closeStart := time.Now()
	closeDone := make(chan error, 1)
	go func() { closeDone <- conn.Close(context.Background()) }()

	// Expect it to return within 5s: disconnectSendTimeout (default 3s) plus some margin.
	// In this scenario only the wait for sending Disconnect is subject to the timeout, so Close
	// should return after one disconnectSendTimeout.
	select {
	case err := <-closeDone:
		assert.NoError(t, err, "the result of wireConn.Close() should be returned as is")
		assert.Less(t, time.Since(closeStart), 5*time.Second,
			"Close should return within disconnectSendTimeout + margin even while a Write is blocked")
	case <-time.After(5 * time.Second):
		t.Fatal("Conn.Close did not return within disconnectSendTimeout + margin while a Write was blocked")
	}

	select {
	case <-openDone:
	case <-time.After(5 * time.Second):
		t.Fatal("OpenUpstream did not return after wireConn.Close() released the underlying pipe")
	}
	<-srvDone
}

// TestConn_CloseDuringWrite verifies that calling Conn.Close while continuously calling
// Upstream.WriteDataPoints returns in finite time.
//
// The existing TestUpstream_ClientConnClose (upstream_test.go) intentionally avoids the race
// between Close and WriteDataPoints by inserting time.After(100µs) (see the TODO in the code),
// whereas this test goes after that race through repetition.
func TestConn_CloseDuringWrite(t *testing.T) {
	defer goleak.VerifyNone(t)
	d := newDialer(transport.NegotiationParams{})
	RegisterDialer(TransportTest, func() transport.Dialer { return d })

	srvDone := make(chan struct{})
	go func() {
		defer close(srvDone)
		mockConnectRequest(t, d.srv)
		upstreamOpenReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamOpenRequest)
		mustWrite(t, d.srv, &message.UpstreamOpenResponse{
			RequestID:             upstreamOpenReq.RequestID,
			AssignedStreamID:      uuid.New(),
			AssignedStreamIDAlias: 1,
			ResultCode:            message.ResultCodeSucceeded,
			ResultString:          "OK",
			DataIDAliases:         map[uint32]*message.DataID{},
		})
		// From here on, discard chunks etc. until Disconnect arrives.
		for {
			msg, err := d.srv.ReadMessage()
			if err != nil {
				return
			}
			if _, ok := msg.(*message.Disconnect); ok {
				return
			}
		}
	}()

	conn, err := Connect("dummy", TransportTest)
	require.NoError(t, err)

	up, err := conn.OpenUpstream(context.Background(), "session_id",
		WithUpstreamQoS(message.QoSUnreliable),
	)
	require.NoError(t, err)

	dataID := &message.DataID{Name: "name", Type: "type"}
	stopWrite := make(chan struct{})
	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		for {
			select {
			case <-stopWrite:
				return
			default:
				_ = up.WriteDataPoints(context.Background(), dataID, &message.DataPoint{
					ElapsedTime: time.Second,
					Payload:     []byte{1, 2, 3, 4},
				})
			}
		}
	}()

	closeDone := make(chan error, 1)
	go func() { closeDone <- conn.Close(context.Background()) }()

	select {
	case err := <-closeDone:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Conn.Close did not return within timeout while WriteDataPoints loop is running")
	}

	close(stopWrite)
	<-writeDone
	<-srvDone
}
