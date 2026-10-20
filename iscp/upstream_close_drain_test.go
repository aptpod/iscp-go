package iscp_test

import (
	"context"
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

// TestUpstream_Close_LateAckAfterAckTimeoutDoesNotBurnCloseTimeout verifies that, when the Ack
// of a chunk whose waiter left on Ack timeout arrives late, the subsequent Close does not use
// up closeTimeout (regression test).
//
// If removal from sentBuf (the set of un-acked chunks) is done only by sendChunkAndWaitAck (the
// waiter), nobody reflects an Ack that arrives after the waiter left on timeout into sentBuf.
// Then the drain in Close (waits until listSent() becomes empty) can never be satisfied: even
// though the server correctly returns the Ack, Close uses up closeDeadline every time and a
// cutoff warning is logged although no chunk is in flight. By driving the removal from the
// arrival of the Ack (processResult), sentBuf reflects the Ack regardless of whether the waiter
// is alive.
func TestUpstream_Close_LateAckAfterAckTimeoutDoesNotBurnCloseTimeout(t *testing.T) {
	defer goleak.VerifyNone(t)

	d := newDialer(transport.NegotiationParams{})
	RegisterDialer(TransportTest, func() transport.Dialer { return d })

	sendLateAck := make(chan struct{})
	srvDone := make(chan struct{})
	go func() {
		defer close(srvDone)
		// Disable keepalive with the v4 handshake (prevents a ping timeout disconnect while the
		// server mock is not reading).
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
		WithUpstreamCloseTimeout(2*time.Second),
		WithUpstreamReceiveAckHooker(hooker),
	)
	require.NoError(t, err)

	dataID := &message.DataID{Name: "name", Type: "type"}
	dp := &message.DataPoint{ElapsedTime: time.Second, Payload: []byte{1, 2, 3, 4}}
	require.NoError(t, up.WriteDataPoints(ctx, dataID, dp))
	require.NoError(t, up.Flush(ctx))

	// Guarantee that AckTimeout(10ms) fires (the waiter leaves) before delivering the late Ack.
	// This sleep is a lower bound that guarantees the event happens, not an upper-bound
	// assertion, so it does not make the test flaky.
	time.Sleep(300 * time.Millisecond)
	close(sendLateAck)

	// Verify that the late Ack was processed (HookAfter is queued on readResultLoop,
	// so arrival = progress of Ack processing).
	select {
	case <-hooker.afterReceivedAckCh:
	case <-time.After(5 * time.Second):
		t.Fatal("late ack did not arrive")
	}

	// Oracle: if the late Ack is reflected in sentBuf, the drain in Close is satisfied
	// immediately and Close returns right away. If it is not reflected (before the fix), the drain
	// uses up closeTimeout(2s).
	start := time.Now()
	require.NoError(t, up.Close(ctx))
	assert.Less(t, time.Since(start), 1500*time.Millisecond,
		"Close should not burn closeTimeout waiting for a chunk whose ack already arrived")

	require.NoError(t, conn.Close(ctx))
	<-srvDone
}
