package iscp_test

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/aptpod/iscp-go/v2/iscp"
	. "github.com/aptpod/iscp-go/v2/iscp"
	"github.com/aptpod/iscp-go/v2/message"
	"github.com/aptpod/iscp-go/v2/transport"
)

// mockUpstreamOpen responds to UpstreamOpenRequest with AssignedStreamIDAlias=1.
func mockUpstreamOpen(t *testing.T, srv *transport.MessageTransport) {
	openReq := mustRead(t, srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamOpenRequest)
	mustWrite(t, srv, &message.UpstreamOpenResponse{
		RequestID:             openReq.RequestID,
		AssignedStreamID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		AssignedStreamIDAlias: 1,
		ResultCode:            message.ResultCodeSucceeded,
		ResultString:          "OK",
		DataIDAliases:         map[uint32]*message.DataID{},
	})
}

// mockResumeAndClose responds to UpstreamResumeRequest / UpstreamCloseRequest /
// Disconnect after a reconnect.
func mockResumeAndClose(t *testing.T, srv *transport.MessageTransport) {
	msg := mustRead(t, srv, &message.Ping{}, &message.Pong{})
	resumeReq, ok := msg.(*message.UpstreamResumeRequest)
	require.True(t, ok, "%T", msg)
	mustWrite(t, srv, &message.UpstreamResumeResponse{
		RequestID:             resumeReq.RequestID,
		AssignedStreamIDAlias: 1,
		ResultCode:            message.ResultCodeSucceeded,
		ResultString:          "OK",
		ExtensionFields:       &message.UpstreamResumeResponseExtensionFields{},
	})
	closeReq := mustRead(t, srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamCloseRequest)
	mustWrite(t, srv, &message.UpstreamCloseResponse{
		RequestID:    closeReq.RequestID,
		ResultCode:   message.ResultCodeSucceeded,
		ResultString: "OK",
	})
	mustRead(t, srv, &message.Ping{}, &message.Pong{})
}

// There is no dedicated red test for turning the bare send to resCh in readAckLoop into a select.
// A persistently full resCh requires the reader (readResultLoop) to stall; the only cause for
// readResultLoop to stall (with resultCh capacity 1) is the u.mu wait in processResult,
// but readAckLoop takes the same u.mu in processDataIDAliases right before sending to resCh,
// so if u.mu is held to stop readResultLoop, readAckLoop also stops on u.mu before reaching the
// resCh send.
// In other words, there is no external synchronization point that deterministically creates a
// state of being blocked on the resCh send and never released (see the comment on the select in
// readAckLoop for details).
// The select is a defense in depth against another stall cause being added to readResultLoop in
// the future, and TestUpstream_RunWaitsForReadResultLoopExit verifies that eg.Wait() completes.

// TestUpstream_RunWaitsForReadResultLoopExit verifies that run() returns only after
// readResultLoop has exited (regression test).
//
// If readResultLoop is outside the errgroup (started with go), run() returns without waiting for
// readResultLoop to exit, and runWg.Wait() in resume() also passes straight through. If the defer
// of the surviving readResultLoop (closing all upstreamChunkResultChs + recreating the map)
// closes live channels of the next session generation, sendChunkAndWaitAck waiting for that ack
// receives nil from the closed channel and proceeds to removeSent, dropping a chunk whose ack has
// not arrived from the QoS Reliable resend set as if it were acked (data loss). If run() does not
// return until readResultLoop exits, there is structurally no window in which the defer runs
// across generations.
//
// The state "readResultLoop has not exited although all other errgroup members have" is created
// by queueing a large number of Results and then closing the server-side pipe. The pipe is FIFO,
// so the client detects EOF (disconnect) only after reading all 8 acks. Draining in
// readResultLoop (hundreds of thousands of processResult calls) is long enough compared with the
// teardown (a few ms), so readResultLoop is certainly still draining when run() returns.
func TestUpstream_RunWaitsForReadResultLoopExit(t *testing.T) {
	defer goleak.VerifyNone(t)

	d1 := newDialer(transport.NegotiationParams{})
	d2 := newDialer(transport.NegotiationParams{})
	registerTestTransport(t, []*dialer{d1, d2})

	// Number of Results per message. 8 messages make 400k processResult calls, which creates a drain
	// time much longer than the teardown (no side effects since the seq does not exist).
	const resultsPerAck = 50000

	sendProbe := make(chan struct{})
	sendGarbage := make(chan struct{})
	srv1Done := make(chan struct{})
	go func() {
		defer close(srv1Done)
		// Disable keepalive with the v4 handshake (prevents a ping timeout disconnect while the
		// server mock is not reading).
		mockConnectRequestV4(t, d1.srv)
		mockUpstreamOpen(t, d1.srv)
		// Probe: use the arrival of this ack at ReceiveAckHooker to confirm that run() and its
		// goroutines (runWg.Add done) are running. Calling WaitRunDoneForTest before the Add(1) in run()
		// makes runWg.Wait() return immediately, so this synchronization is needed. Delay sending until
		// sendProbe (OpenUpstream on the main side completed = subscription for alias 1 registered).
		// If sent right after the open response, an ack arriving before the subscription is registered
		// is dropped and the probe may not be observed.
		<-sendProbe
		mustWrite(t, d1.srv, &message.UpstreamChunkAck{
			StreamIDAlias: 1,
			Results: []*message.UpstreamChunkResult{
				{SequenceNumber: 9998, ResultCode: message.ResultCodeSucceeded, ResultString: "OK"},
			},
			DataIDAliases:   map[uint32]*message.DataID{},
			ExtensionFields: &message.UpstreamChunkAckExtensionFields{},
		})
		<-sendGarbage
		results := make([]*message.UpstreamChunkResult, resultsPerAck)
		for i := range results {
			// A sequence number that does not exist. processResult returns early on !ok,
			// but it takes u.mu for each entry, so it still consumes drain time.
			results[i] = &message.UpstreamChunkResult{
				SequenceNumber: 9999, ResultCode: message.ResultCodeSucceeded, ResultString: "OK",
			}
		}
		for i := 0; i < 8; i++ {
			mustWrite(t, d1.srv, &message.UpstreamChunkAck{
				StreamIDAlias:   1,
				Results:         results,
				DataIDAliases:   map[uint32]*message.DataID{},
				ExtensionFields: &message.UpstreamChunkAckExtensionFields{},
			})
		}
		// Close the server side right after the 8 messages. The pipe is FIFO, so the client detects
		// EOF only after reading all 8 messages (the disconnect does not overtake the acks).
		_ = d1.srv.Close()
	}()

	// Delay the response to the reconnect ConnectRequest until allowReconnect. This keeps connState
	// at Reconnecting after the disconnect, so waitForReconnecting in run() reliably detects it and
	// cancels the errgroup ctx (if the response is fast, the Reconnecting period is only during the
	// dial and level detection may miss it). It also guarantees that nothing after resume (the read
	// loops of the new session) has started at the time the oracle is evaluated.
	allowReconnect := make(chan struct{})
	srv2Done := make(chan struct{})
	go func() {
		defer close(srv2Done)
		<-allowReconnect
		mockConnectRequestV4(t, d2.srv)
		mockResumeAndClose(t, d2.srv)
	}()

	ctx := context.Background()
	conn, err := Connect("dummy", TransportTest,
		iscp.WithConnNodeID("11111111-1111-1111-1111-111111111111"),
	)
	require.NoError(t, err)

	hooker := NewCaptureHooker()
	resumedEvCh := make(chan *UpstreamResumedEvent, 1)
	up, err := conn.OpenUpstream(ctx,
		"session_id",
		WithUpstreamQoS(message.QoSUnreliable),
		WithUpstreamFlushPolicyNone(),
		WithUpstreamReceiveAckHooker(hooker),
		WithUpstreamResumedEventHandler(UpstreamResumedEventHandlerFunc(func(ev *UpstreamResumedEvent) {
			select {
			case resumedEvCh <- ev:
			default:
			}
		})),
	)
	require.NoError(t, err)

	// Arrival of the probe ack = readResultLoop processed one entry = run() has done
	// runWg.Add(1). Do not call WaitRunDoneForTest before this point.
	close(sendProbe)
	select {
	case <-hooker.afterReceivedAckCh:
	case <-time.After(5 * time.Second):
		t.Fatal("probe ack did not arrive")
	}
	runDone := up.WaitRunDoneForTest()

	// Feed the large number of Results -> proceed to server-side close (disconnect).
	close(sendGarbage)

	// Wait for run() to finish. Members other than readResultLoop (flushLoop /
	// readAckLoop / waitForReconnecting) finish within a few ms at the disconnect teardown.
	select {
	case <-runDone:
	case <-time.After(10 * time.Second):
		t.Fatal("run() did not return after disconnect")
	}

	// Oracle: readResultLoop must have exited by the time run() returns. If it were outside the
	// errgroup, run() would return leaving readResultLoop mid-drain, and its defer could close live
	// channels of the post-resume generation (a window for data loss).
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	if strings.Contains(string(buf[:n]), ".readResultLoop(") {
		t.Fatal("run() returned while readResultLoop is still running; readResultLoop must be part of the errgroup")
	}

	// Cleanup: let the reconnect proceed and confirm the resume completes.
	close(allowReconnect)
	select {
	case <-resumedEvCh:
	case <-time.After(5 * time.Second):
		t.Fatal("resume did not complete")
	}
	require.NoError(t, up.Close(ctx))
	require.NoError(t, conn.Close(ctx))
	<-srv1Done
	<-srv2Done
}
