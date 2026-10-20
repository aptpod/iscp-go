package iscp_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	. "github.com/aptpod/iscp-go/v2/iscp"
	"github.com/aptpod/iscp-go/v2/message"
	"github.com/aptpod/iscp-go/v2/transport"
)

var errWriteFailInjected = errors.New("test: write failure injected")

// writeFailTransport is a wrapper that makes the Write of transport.Transport fail only once.
// The first Write that consumes armed returns an error and notifies failed.
// Other Writes pass straight through to the lower layer.
type writeFailTransport struct {
	transport.Transport
	armed  atomic.Bool
	failed chan struct{}
}

func (w *writeFailTransport) Write(b []byte) error {
	if w.armed.CompareAndSwap(true, false) {
		close(w.failed)
		return errWriteFailInjected
	}
	return w.Transport.Write(b)
}

// TestUpstream_LateAckAfterSendErrorDoesNotBlockMutex verifies that, even if the Ack for a
// sequence number arrives after sendChunkAndWaitAck abandoned receiving from resultCh due to a
// send error, processResult does not block while holding u.mu (the main path reachable with the
// default configuration).
//
// Trigger condition: on an error from SendEncodedUpstreamChunk, sendChunkAndWaitAck returns leaving
// the entry in upstreamChunkResultChs. If resultCh is unbuffered, when the Ack for that seq
// arrives later, processResult stops at ch <- result while holding u.mu (writer), and flush /
// WriteDataPoints / Close / readResultLoop (all subsequent Ack processing) stop in a chain.
// The situation "the send failed but the server returns an Ack" really occurs with the partially
// sent error of the multi transport or a write timeout (an error after data was put in the buffer).
//
// Oracle: write a second chunk after delivering the late Ack, and its Ack must reach
// ReceiveAckHooker. Before the fix, readResultLoop blocks together with processResult on the first
// late Ack, so neither the write of the second chunk nor Ack processing progresses, and the test
// FAILs by timeout.
func TestUpstream_LateAckAfterSendErrorDoesNotBlockMutex(t *testing.T) {
	defer goleak.VerifyNone(t)

	d := newDialer(transport.NegotiationParams{})
	wft := &writeFailTransport{Transport: d, failed: make(chan struct{})}
	RegisterDialer(TransportTest, func() transport.Dialer {
		return transport.DialerFunc(func(c transport.DialConfig) (transport.Transport, error) {
			if _, err := d.Dial(c); err != nil {
				return nil, err
			}
			return wft, nil
		})
	})

	// Drive the server side progress with phase synchronization channels (do not wait with sleep).
	sendGhostAck := make(chan struct{})
	srvDone := make(chan struct{})
	go func() {
		defer close(srvDone)
		// Handshake with v4 to disable iSCP-level Ping/Pong (needsPingPong becomes false). The server
		// mock in this test does not read while waiting on the phase synchronization channels, so if a
		// ping arrived in the meantime it could not return a Pong and the connection would be dropped by
		// PingTimeout (default 1s).
		// It also prevents the Write of a ping from wrongly consuming the injected error of
		// writeFailTransport.
		mockConnectRequestV4(t, d.srv)
		openReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamOpenRequest)
		mustWrite(t, d.srv, &message.UpstreamOpenResponse{
			RequestID:             openReq.RequestID,
			AssignedStreamID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			AssignedStreamIDAlias: 1,
			ResultCode:            message.ResultCodeSucceeded,
			ResultString:          "OK",
			DataIDAliases:         map[uint32]*message.DataID{},
		})

		// chunk(seq=1) is not read because it does not reach the wire due to the injected Write error.
		// The server can send an Ack at any time by protocol, so push the Ack for seq=1
		// (reproducing the case where it was partially sent and the server accepted it).
		<-sendGhostAck
		mustWrite(t, d.srv, &message.UpstreamChunkAck{
			StreamIDAlias: 1,
			Results: []*message.UpstreamChunkResult{
				{SequenceNumber: 1, ResultCode: message.ResultCodeSucceeded, ResultString: "OK"},
			},
			DataIDAliases:   map[uint32]*message.DataID{},
			ExtensionFields: &message.UpstreamChunkAckExtensionFields{},
		})

		// The second chunk (seq=2) reaches the wire normally, so read it and return an Ack.
		chunk := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamChunk)
		mustWrite(t, d.srv, &message.UpstreamChunkAck{
			StreamIDAlias: 1,
			Results: []*message.UpstreamChunkResult{
				{SequenceNumber: chunk.StreamChunk.SequenceNumber, ResultCode: message.ResultCodeSucceeded, ResultString: "OK"},
			},
			DataIDAliases:   map[uint32]*message.DataID{},
			ExtensionFields: &message.UpstreamChunkAckExtensionFields{},
		})

		closeReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamCloseRequest)
		mustWrite(t, d.srv, &message.UpstreamCloseResponse{
			RequestID:    closeReq.RequestID,
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "OK",
		})
		mustRead(t, d.srv, &message.Ping{}, &message.Pong{})
	}()

	ctx := context.Background()
	conn, err := Connect("dummy", TransportTest)
	require.NoError(t, err)

	hooker := NewCaptureHooker()
	up, err := conn.OpenUpstream(ctx,
		"session_id",
		WithUpstreamQoS(message.QoSUnreliable),
		WithUpstreamFlushPolicyNone(),
		WithUpstreamReceiveAckHooker(hooker),
	)
	require.NoError(t, err)

	dataID := &message.DataID{Name: "name", Type: "type"}
	dp := &message.DataPoint{ElapsedTime: time.Second, Payload: []byte{1, 2, 3, 4}}

	// First chunk: make Write fail with the injected error. sendChunkAndWaitAck returns without
	// receiving from resultCh, and upstreamChunkResultChs[1] remains.
	wft.armed.Store(true)
	require.NoError(t, up.WriteDataPoints(ctx, dataID, dp))
	require.NoError(t, up.Flush(ctx))
	select {
	case <-wft.failed:
	case <-time.After(5 * time.Second):
		t.Fatal("injected write failure did not fire")
	}

	// Deliver the Ack for seq=1 whose reader is gone.
	close(sendGhostAck)

	// Oracle: the write of the second chunk and Ack processing must progress. Before the fix,
	// processResult is stopped at ch <- result while holding u.mu, so WriteDataPoints / flush wait
	// on u.mu and this does not progress.
	// require.* calls t.FailNow (runtime.Goexit) and thus cannot be used from goroutines other than
	// the test goroutine. Pass errors to the main body through a channel and fail there.
	oracleErr := make(chan error, 1)
	oracleDone := make(chan struct{})
	go func() {
		defer close(oracleDone)
		if err := up.WriteDataPoints(ctx, dataID, dp); err != nil {
			oracleErr <- err
			return
		}
		if err := up.Flush(ctx); err != nil {
			oracleErr <- err
			return
		}
		for {
			ack := <-hooker.afterReceivedAckCh
			if ack.Sequence == 2 {
				return
			}
		}
	}()
	select {
	case <-oracleDone:
		select {
		case err := <-oracleErr:
			t.Fatalf("oracle goroutine failed: %v", err)
		default:
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second chunk was not processed: processResult is blocking with u.mu held")
	}

	require.NoError(t, up.Close(ctx))
	require.NoError(t, conn.Close(ctx))
	<-srvDone
}

// TestUpstream_LateAckAfterAckTimeoutDoesNotBlockMutex verifies that, even if the Ack for a
// sequence number arrives late after sendChunkAndWaitAck abandoned receiving from resultCh due to
// the Ack timeout (WithUpstreamAckTimeout, opt-in), processResult does not block while holding
// u.mu.
//
// The oracle is the same as TestUpstream_LateAckAfterSendErrorDoesNotBlockMutex.
// This one differs in that chunk(seq=1) reaches the wire (the server merely delays the Ack).
// The timeout firing cannot be observed from outside, so the firing of AckTimeout(10ms) is
// guaranteed by a sleep with ample margin (300ms). This sleep is a lower bound that guarantees the
// event happened, not an upper-bound assertion, so it does not make the test flaky.
func TestUpstream_LateAckAfterAckTimeoutDoesNotBlockMutex(t *testing.T) {
	defer goleak.VerifyNone(t)

	d := newDialer(transport.NegotiationParams{})
	RegisterDialer(TransportTest, func() transport.Dialer { return d })

	sendLateAck := make(chan struct{})
	srvDone := make(chan struct{})
	go func() {
		defer close(srvDone)
		// The reason for disabling Ping/Pong with the v4 handshake is the same as
		// TestUpstream_LateAckAfterSendErrorDoesNotBlockMutex.
		mockConnectRequestV4(t, d.srv)
		openReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamOpenRequest)
		mustWrite(t, d.srv, &message.UpstreamOpenResponse{
			RequestID:             openReq.RequestID,
			AssignedStreamID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			AssignedStreamIDAlias: 1,
			ResultCode:            message.ResultCodeSucceeded,
			ResultString:          "OK",
			DataIDAliases:         map[uint32]*message.DataID{},
		})

		// Read chunk(seq=1), but do not send the Ack until after the timeout fires.
		first := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamChunk)
		<-sendLateAck
		mustWrite(t, d.srv, &message.UpstreamChunkAck{
			StreamIDAlias: 1,
			Results: []*message.UpstreamChunkResult{
				{SequenceNumber: first.StreamChunk.SequenceNumber, ResultCode: message.ResultCodeSucceeded, ResultString: "OK"},
			},
			DataIDAliases:   map[uint32]*message.DataID{},
			ExtensionFields: &message.UpstreamChunkAckExtensionFields{},
		})

		second := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamChunk)
		mustWrite(t, d.srv, &message.UpstreamChunkAck{
			StreamIDAlias: 1,
			Results: []*message.UpstreamChunkResult{
				{SequenceNumber: second.StreamChunk.SequenceNumber, ResultCode: message.ResultCodeSucceeded, ResultString: "OK"},
			},
			DataIDAliases:   map[uint32]*message.DataID{},
			ExtensionFields: &message.UpstreamChunkAckExtensionFields{},
		})

		closeReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamCloseRequest)
		mustWrite(t, d.srv, &message.UpstreamCloseResponse{
			RequestID:    closeReq.RequestID,
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "OK",
		})
		mustRead(t, d.srv, &message.Ping{}, &message.Pong{})
	}()

	ctx := context.Background()
	conn, err := Connect("dummy", TransportTest)
	require.NoError(t, err)

	hooker := NewCaptureHooker()
	up, err := conn.OpenUpstream(ctx,
		"session_id",
		WithUpstreamQoS(message.QoSUnreliable),
		WithUpstreamFlushPolicyNone(),
		WithUpstreamAckTimeout(10*time.Millisecond),
		WithUpstreamReceiveAckHooker(hooker),
	)
	require.NoError(t, err)

	dataID := &message.DataID{Name: "name", Type: "type"}
	dp := &message.DataPoint{ElapsedTime: time.Second, Payload: []byte{1, 2, 3, 4}}

	require.NoError(t, up.WriteDataPoints(ctx, dataID, dp))
	require.NoError(t, up.Flush(ctx))

	// Guarantee that AckTimeout(10ms) fires before delivering the late Ack.
	time.Sleep(300 * time.Millisecond)
	close(sendLateAck)

	// require.* calls t.FailNow (runtime.Goexit) and thus cannot be used from goroutines other than
	// the test goroutine. Pass errors to the main body through a channel and fail there.
	oracleErr := make(chan error, 1)
	oracleDone := make(chan struct{})
	go func() {
		defer close(oracleDone)
		if err := up.WriteDataPoints(ctx, dataID, dp); err != nil {
			oracleErr <- err
			return
		}
		if err := up.Flush(ctx); err != nil {
			oracleErr <- err
			return
		}
		for {
			ack := <-hooker.afterReceivedAckCh
			if ack.Sequence == 2 {
				return
			}
		}
	}()
	select {
	case <-oracleDone:
		select {
		case err := <-oracleErr:
			t.Fatalf("oracle goroutine failed: %v", err)
		default:
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second chunk was not processed: processResult is blocking with u.mu held")
	}

	require.NoError(t, up.Close(ctx))
	require.NoError(t, conn.Close(ctx))
	<-srvDone
}
