package iscp_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/aptpod/iscp-go/v2/errors"
	"github.com/aptpod/iscp-go/v2/iscp"
	"github.com/aptpod/iscp-go/v2/message"
	"github.com/aptpod/iscp-go/v2/transport"
)

// resendSubSelector routes writes to sub-a until subADown is set, then to sub-b.
type resendSubSelector struct {
	subADown atomic.Bool
}

func (s *resendSubSelector) Get(context.Context, int64) transport.SubConnectionID {
	if s.subADown.Load() {
		return "sub-b"
	}
	return "sub-a"
}

// droppingTransport reports success for the first write after armed is set
// without delivering it, which is how a subconnection loses a chunk whose
// write already succeeded.
type droppingTransport struct {
	transport.Transport
	armed   atomic.Bool
	dropped chan struct{}
}

func (tr *droppingTransport) Write(payload []byte) error {
	if tr.armed.CompareAndSwap(true, false) {
		close(tr.dropped)
		return nil
	}
	return tr.Transport.Write(payload)
}

// multiReliableEnv is a multi-connection Conn with a QoS Reliable upstream on
// two subconnections whose broker side is served by mocks.
type multiReliableEnv struct {
	conn       *iscp.Conn
	up         *iscp.Upstream
	subA       *dialer
	subB       *dialer
	dropper    *droppingTransport
	selector   *resendSubSelector
	subARedial chan struct{}
	hooker     *CaptureHooker
}

func newMultiReliableEnv(t *testing.T, ctx context.Context, opts ...iscp.UpstreamOption) *multiReliableEnv {
	t.Helper()
	env := &multiReliableEnv{
		subA:       newDialer(transport.NegotiationParams{}),
		subB:       newDialer(transport.NegotiationParams{}),
		selector:   &resendSubSelector{},
		subARedial: make(chan struct{}),
		hooker:     NewCaptureHooker(),
	}
	t.Cleanup(func() { _ = env.subA.srv.Close() })
	t.Cleanup(func() { _ = env.subB.srv.Close() })
	env.dropper = &droppingTransport{Transport: env.subA, dropped: make(chan struct{})}

	handshakeDone := make(chan struct{})
	go func() {
		defer close(handshakeDone)
		mockConnectRequestV4(t, env.subA.srv)
		open := mustRead(t, env.subA.srv).(*message.UpstreamOpenRequest)
		mustWrite(t, env.subA.srv, &message.UpstreamOpenResponse{
			RequestID:             open.RequestID,
			AssignedStreamID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			AssignedStreamIDAlias: 1,
			ResultCode:            message.ResultCodeSucceeded,
		})
	}()

	var subADials atomic.Int32
	conn, err := iscp.Connect("dummy", iscp.TransportNameMulti, iscp.WithConnMultiTransport(&iscp.MultiTransportConfig{
		TransportSelector: env.selector,
		ReconnectInterval: 10 * time.Millisecond,
		DialerMap: map[transport.SubConnectionID]transport.Dialer{
			"sub-a": transport.DialerFunc(func(config transport.DialConfig) (transport.Transport, error) {
				switch subADials.Add(1) {
				case 1:
					_, err := env.subA.Dial(config)
					return env.dropper, err
				case 2:
					close(env.subARedial)
				}
				return nil, fmt.Errorf("sub-a remains down")
			}),
			"sub-b": env.subB,
		},
	}))
	require.NoError(t, err)
	env.conn = conn

	opts = append([]iscp.UpstreamOption{
		iscp.WithUpstreamQoS(message.QoSReliable),
		iscp.WithUpstreamReceiveAckHooker(env.hooker),
	}, opts...)
	up, err := conn.OpenUpstream(ctx, "session", opts...)
	require.NoError(t, err)
	env.up = up
	select {
	case <-handshakeDone:
	case <-ctx.Done():
		t.Fatal("handshake did not complete")
	}
	return env
}

func resendTestChunk(payload byte) *iscp.DataPointGroup {
	return &iscp.DataPointGroup{
		DataID:     &message.DataID{Name: "name", Type: "type"},
		DataPoints: []*message.DataPoint{{Payload: []byte{payload}}},
	}
}

// loseChunkOnSubA writes a chunk whose write succeeds but which sub-a drops,
// then takes sub-a down so that later writes go through sub-b while the
// connection as a whole stays up.
func (env *multiReliableEnv) loseChunkOnSubA(t *testing.T, ctx context.Context, payload byte) {
	t.Helper()
	env.dropper.armed.Store(true)
	require.NoError(t, env.up.WriteChunk(ctx, resendTestChunk(payload)))
	select {
	case <-env.dropper.dropped:
	case <-ctx.Done():
		t.Fatal("the chunk did not reach sub-a")
	}
	env.selector.subADown.Store(true)
	require.NoError(t, env.subA.srv.Close())
	select {
	case <-env.subARedial:
	case <-ctx.Done():
		t.Fatal("sub-a did not start reconnecting")
	}
}

func (env *multiReliableEnv) waitAck(t *testing.T, ctx context.Context, seqNum uint32) {
	t.Helper()
	for {
		select {
		case ack := <-env.hooker.afterReceivedAckCh:
			if ack.Sequence == seqNum {
				return
			}
		case <-ctx.Done():
			t.Fatalf("ack of seq %d did not arrive", seqNum)
		}
	}
}

// serveUpstream plays the broker on srv until Disconnect: it acknowledges each
// chunk acks(seq) times (0 drops the chunk silently) and accepts
// UpstreamCloseRequest. It returns every message it read, in order.
func serveUpstream(srv *transport.MessageTransport, acks func(seqNum uint32) int) <-chan []message.Message {
	done := make(chan []message.Message, 1)
	go func() {
		var got []message.Message
		defer func() { done <- got }()
		for {
			msg, err := srv.ReadMessage()
			if err != nil {
				return
			}
			got = append(got, msg)
			switch m := msg.(type) {
			case *message.UpstreamChunk:
				seqNum := m.StreamChunk.SequenceNumber
				for range acks(seqNum) {
					if err := srv.WriteMessage(&message.UpstreamChunkAck{
						StreamIDAlias: 1,
						Results: []*message.UpstreamChunkResult{
							{SequenceNumber: seqNum, ResultCode: message.ResultCodeSucceeded},
						},
					}); err != nil {
						return
					}
				}
			case *message.UpstreamCloseRequest:
				if err := srv.WriteMessage(&message.UpstreamCloseResponse{
					RequestID: m.RequestID, ResultCode: message.ResultCodeSucceeded,
				}); err != nil {
					return
				}
			case *message.Disconnect:
				return
			}
		}
	}()
	return done
}

func chunkPayload(c *message.UpstreamChunk) []byte {
	return c.StreamChunk.DataPointGroups[0].DataPoints[0].Payload
}

func TestUpstream_MultiReliable_ResendsChunkLostOnSubconnection(t *testing.T) {
	iscp.SetMinResendInterval(t, 300*time.Millisecond)
	for _, ackTimeout := range []time.Duration{0, 20 * time.Millisecond} {
		t.Run(fmt.Sprintf("ack_timeout_%s", ackTimeout), func(t *testing.T) {
			// The resend loop must end with the stream instead of outliving Close.
			defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			env := newMultiReliableEnv(t, ctx, iscp.WithUpstreamAckTimeout(ackTimeout), iscp.WithUpstreamCloseTimeout(5*time.Second))
			// Ack the resent seq 1 twice: the second one stands for a late Ack of the
			// original send racing with the resend.
			served := serveUpstream(env.subB.srv, func(seqNum uint32) int {
				if seqNum == 1 {
					return 2
				}
				return 1
			})

			env.loseChunkOnSubA(t, ctx, 1)
			require.NoError(t, env.up.WriteChunk(ctx, resendTestChunk(2)))
			env.waitAck(t, ctx, 2)

			// Close starts while seq 1 is unacknowledged; it must wait for the resend
			// and its Ack instead of returning on the Ack of the last chunk.
			require.NoError(t, env.up.Close(ctx))
			require.NoError(t, env.conn.Close(ctx))

			var resentAt, closeAt int
			for i, msg := range <-served {
				switch m := msg.(type) {
				case *message.UpstreamChunk:
					if m.StreamChunk.SequenceNumber == 1 && resentAt == 0 {
						resentAt = i + 1
						assert.Equal(t, []byte{1}, chunkPayload(m), "resend must carry the same data")
					}
				case *message.UpstreamCloseRequest:
					closeAt = i + 1
					assert.EqualValues(t, 2, m.FinalSequenceNumber)
				}
			}
			require.NotZero(t, resentAt, "seq 1 was not resent on the surviving subconnection")
			require.NotZero(t, closeAt, "CloseRequest was not sent")
			assert.Less(t, resentAt, closeAt, "seq 1 must be resent before CloseRequest")
		})
	}
}

func TestUpstream_MultiReliable_CloseFailsWithUnackedChunks(t *testing.T) {
	tests := []struct {
		name         string
		closeTimeout time.Duration
		wantResend   bool
	}{
		{name: "deadline expires while resending", closeTimeout: 600 * time.Millisecond, wantResend: true},
		// The default minimum resend wait (5s) does not elapse because Close does not wait.
		{name: "close timeout 0 does not wait", closeTimeout: 0, wantResend: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.wantResend {
				iscp.SetMinResendInterval(t, 100*time.Millisecond)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			env := newMultiReliableEnv(t, ctx, iscp.WithUpstreamCloseTimeout(tt.closeTimeout))
			// The broker never acknowledges seq 1, including its resends.
			served := serveUpstream(env.subB.srv, func(seqNum uint32) int {
				if seqNum == 1 {
					return 0
				}
				return 1
			})

			env.loseChunkOnSubA(t, ctx, 1)
			require.NoError(t, env.up.WriteChunk(ctx, resendTestChunk(2)))
			env.waitAck(t, ctx, 2)

			err := env.up.Close(ctx)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "1 unacknowledged chunks")
			var failed errors.FailedMessageError
			assert.NotErrorAs(t, err, &failed, "the broker accepted the close; the error is local")
			require.NoError(t, env.conn.Close(ctx))

			var resends int
			var closeSent bool
			for _, msg := range <-served {
				switch m := msg.(type) {
				case *message.UpstreamChunk:
					if m.StreamChunk.SequenceNumber == 1 {
						resends++
					}
				case *message.UpstreamCloseRequest:
					closeSent = true
				}
			}
			assert.True(t, closeSent, "CloseRequest must still be sent to the broker")
			if tt.wantResend {
				assert.Positive(t, resends, "seq 1 must be resent while Close waits")
			} else {
				assert.Zero(t, resends)
			}
		})
	}
}

func TestUpstream_MultiReliable_CloseSucceedsWithoutUnackedChunks(t *testing.T) {
	tests := []struct {
		name   string
		chunks int
	}{
		{name: "empty stream", chunks: 0},
		{name: "all chunks acknowledged", chunks: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			env := newMultiReliableEnv(t, ctx)
			served := serveUpstream(env.subA.srv, func(uint32) int { return 1 })

			for i := range tt.chunks {
				require.NoError(t, env.up.WriteChunk(ctx, resendTestChunk(byte(i+1))))
				env.waitAck(t, ctx, uint32(i+1))
			}
			require.NoError(t, env.up.Close(ctx))
			require.NoError(t, env.conn.Close(ctx))
			<-served
		})
	}
}

// TestUpstream_SingleReliable_CloseKeepsReturningOnLastAck pins that the
// resend and the wait for every Ack stay limited to the multi-connection
// transport: on a single connection Close still returns once the last chunk
// is acknowledged, without resending an earlier unacknowledged chunk.
func TestUpstream_SingleReliable_CloseKeepsReturningOnLastAck(t *testing.T) {
	iscp.SetMinResendInterval(t, 50*time.Millisecond)
	d := newDialer(transport.NegotiationParams{})
	iscp.RegisterDialer(TransportTest, func() transport.Dialer { return d })

	handshakeDone := make(chan struct{})
	go func() {
		defer close(handshakeDone)
		mockConnectRequestV4(t, d.srv)
		open := mustRead(t, d.srv).(*message.UpstreamOpenRequest)
		mustWrite(t, d.srv, &message.UpstreamOpenResponse{
			RequestID:             open.RequestID,
			AssignedStreamID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			AssignedStreamIDAlias: 1,
			ResultCode:            message.ResultCodeSucceeded,
		})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := iscp.Connect("dummy", TransportTest)
	require.NoError(t, err)
	hooker := NewCaptureHooker()
	up, err := conn.OpenUpstream(ctx, "session", iscp.WithUpstreamQoS(message.QoSReliable), iscp.WithUpstreamReceiveAckHooker(hooker))
	require.NoError(t, err)
	<-handshakeDone
	served := serveUpstream(d.srv, func(seqNum uint32) int {
		if seqNum == 1 {
			return 0
		}
		return 1
	})

	require.NoError(t, up.WriteChunk(ctx, resendTestChunk(1)))
	require.NoError(t, up.WriteChunk(ctx, resendTestChunk(2)))
	select {
	case ack := <-hooker.afterReceivedAckCh:
		require.EqualValues(t, 2, ack.Sequence)
	case <-ctx.Done():
		t.Fatal("ack of seq 2 did not arrive")
	}
	// Leave more than the shortened resend wait so that a resend would show up,
	// and let the waiter of seq 2 record it as the last acknowledged chunk.
	time.Sleep(200 * time.Millisecond)

	require.NoError(t, up.Close(ctx))
	require.NoError(t, conn.Close(ctx))

	var seq1 int
	for _, msg := range <-served {
		if m, ok := msg.(*message.UpstreamChunk); ok && m.StreamChunk.SequenceNumber == 1 {
			seq1++
		}
	}
	assert.Equal(t, 1, seq1, "a single connection must not resend while connected")
}

func TestUpstream_MultiReliable_CloseWithCanceledContextStopsResending(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	iscp.SetMinResendInterval(t, 50*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// The first resend wait is also bounded below by twice the ack interval.
	env := newMultiReliableEnv(t, ctx, iscp.WithUpstreamAckInterval(10*time.Millisecond), iscp.WithUpstreamCloseTimeout(5*time.Second))
	served := serveUpstream(env.subB.srv, func(seqNum uint32) int {
		if seqNum == 1 {
			return 0
		}
		return 1
	})

	env.loseChunkOnSubA(t, ctx, 1)
	require.NoError(t, env.up.WriteChunk(ctx, resendTestChunk(2)))
	env.waitAck(t, ctx, 2)

	closeCtx, closeCancel := context.WithCancel(ctx)
	time.AfterFunc(200*time.Millisecond, closeCancel)
	start := time.Now()
	require.Error(t, env.up.Close(closeCtx))
	assert.Less(t, time.Since(start), 2*time.Second, "Close must return on cancellation, not on the close timeout")

	// After Close returns, the stream no longer resends seq 1.
	time.Sleep(200 * time.Millisecond)
	require.NoError(t, env.conn.Close(ctx))
	msgs := <-served
	var lastResend, closeAt int
	for i, msg := range msgs {
		switch m := msg.(type) {
		case *message.UpstreamChunk:
			if m.StreamChunk.SequenceNumber == 1 {
				lastResend = i + 1
			}
		case *message.UpstreamCloseRequest:
			closeAt = i + 1
		}
	}
	assert.Positive(t, lastResend, "seq 1 must be resent before the cancellation")
	if closeAt > 0 {
		assert.Less(t, lastResend, closeAt, "no resend may follow CloseRequest")
	}
}

// TestUpstream_MultiReliable_CloseCountsUnsentDataPoints covers data that is
// still in the send buffer when Close skips the drain: it is not acknowledged
// either, so Close must not report success unless the broker acknowledged it.
func TestUpstream_MultiReliable_CloseCountsUnsentDataPoints(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	env := newMultiReliableEnv(t, ctx, iscp.WithUpstreamCloseTimeout(0), iscp.WithUpstreamFlushPolicyNone())
	served := serveUpstream(env.subA.srv, func(uint32) int { return 1 })

	require.NoError(t, env.up.WriteDataPoints(ctx, &message.DataID{Name: "name", Type: "type"}, &message.DataPoint{Payload: []byte{1}}))
	closeErr := env.up.Close(ctx)
	require.NoError(t, env.conn.Close(ctx))

	var chunks int
	for _, msg := range <-served {
		if _, ok := msg.(*message.UpstreamChunk); ok {
			chunks++
		}
	}
	if closeErr == nil {
		// Success is only allowed when the data reached the broker and was acknowledged.
		assert.Equal(t, 1, chunks, "Close succeeded although the data was never sent")
		return
	}
	assert.Contains(t, closeErr.Error(), "unacknowledged")
}

// TestUpstream_MultiReliable_CloseAfterConnCloseKeepsSucceeding pins that the
// unacknowledged check applies only to a Close that sent the CloseRequest
// itself: after Conn.Close has torn the stream down, Close returns nil as
// before.
func TestUpstream_MultiReliable_CloseAfterConnCloseKeepsSucceeding(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	env := newMultiReliableEnv(t, ctx)
	served := serveUpstream(env.subA.srv, func(uint32) int { return 0 })

	require.NoError(t, env.up.WriteChunk(ctx, resendTestChunk(1)))
	require.NoError(t, env.conn.Close(ctx))
	<-served
	require.NoError(t, env.up.Close(ctx))
}

// TestUpstream_MultiReliable_WholeReconnectHandsOverToResume covers the loss
// of every subconnection: the connection as a whole reconnects, the resend
// while connected stops with the old session, and the unacknowledged chunk is
// resent by the resume on the new session.
func TestUpstream_MultiReliable_WholeReconnectHandsOverToResume(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	// Let a resend come due on the old session while it is down.
	iscp.SetMinResendInterval(t, 30*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	oldSub := newDialer(transport.NegotiationParams{})
	newSub := newDialer(transport.NegotiationParams{})
	t.Cleanup(func() { _ = oldSub.srv.Close() })
	t.Cleanup(func() { _ = newSub.srv.Close() })
	dropper := &droppingTransport{Transport: oldSub, dropped: make(chan struct{})}

	go func() {
		mockConnectRequestV4(t, oldSub.srv)
		open := mustRead(t, oldSub.srv).(*message.UpstreamOpenRequest)
		mustWrite(t, oldSub.srv, &message.UpstreamOpenResponse{
			RequestID:             open.RequestID,
			AssignedStreamID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			AssignedStreamIDAlias: 1,
			ResultCode:            message.ResultCodeSucceeded,
		})
	}()
	resumed := make(chan (<-chan []message.Message), 1)
	go func() {
		mockConnectRequestV4(t, newSub.srv)
		req := mustRead(t, newSub.srv).(*message.UpstreamResumeRequest)
		mustWrite(t, newSub.srv, &message.UpstreamResumeResponse{
			RequestID:             req.RequestID,
			AssignedStreamIDAlias: 1,
			ResultCode:            message.ResultCodeSucceeded,
		})
		resumed <- serveUpstream(newSub.srv, func(uint32) int { return 1 })
	}()

	// The old connection gets oldSub; after the whole connection is reported
	// disconnected (the old multi transport is closed), the next dial gets newSub.
	var dials atomic.Int32
	var disconnected atomic.Bool
	conn, err := iscp.Connect("dummy", iscp.TransportNameMulti,
		iscp.WithConnDisconnectedEventHandler(iscp.DisconnectedEventHandlerFunc(func(*iscp.DisconnectedEvent) {
			disconnected.Store(true)
		})),
		iscp.WithConnMultiTransport(&iscp.MultiTransportConfig{
			MaxReconnectAttempts: 3,
			ReconnectInterval:    20 * time.Millisecond,
			DialerMap: map[transport.SubConnectionID]transport.Dialer{
				"sub-a": transport.DialerFunc(func(config transport.DialConfig) (transport.Transport, error) {
					if dials.Add(1) == 1 {
						_, err := oldSub.Dial(config)
						return dropper, err
					}
					if disconnected.Load() {
						return newSub.Dial(config)
					}
					return nil, fmt.Errorf("sub-a is down")
				}),
			},
		}))
	require.NoError(t, err)
	hooker := NewCaptureHooker()
	up, err := conn.OpenUpstream(ctx, "session",
		iscp.WithUpstreamQoS(message.QoSReliable),
		iscp.WithUpstreamAckInterval(10*time.Millisecond),
		iscp.WithUpstreamCloseTimeout(5*time.Second),
		iscp.WithUpstreamReceiveAckHooker(hooker),
	)
	require.NoError(t, err)

	dropper.armed.Store(true)
	require.NoError(t, up.WriteChunk(ctx, resendTestChunk(1)))
	select {
	case <-dropper.dropped:
	case <-ctx.Done():
		t.Fatal("the chunk did not reach sub-a")
	}
	require.NoError(t, oldSub.srv.Close())

	var served <-chan []message.Message
	select {
	case served = <-resumed:
	case <-ctx.Done():
		t.Fatal("the upstream was not resumed on the new connection")
	}
	// A Close during the resume does not wait for Acks (existing behavior), so
	// close after the resend on the new session is acknowledged.
	select {
	case ack := <-hooker.afterReceivedAckCh:
		require.EqualValues(t, 1, ack.Sequence)
	case <-ctx.Done():
		t.Fatal("ack of seq 1 did not arrive on the new session")
	}
	require.NoError(t, up.Close(ctx))
	require.NoError(t, conn.Close(ctx))

	var seq1 int
	var closeSent bool
	for _, msg := range <-served {
		switch m := msg.(type) {
		case *message.UpstreamChunk:
			if m.StreamChunk.SequenceNumber == 1 {
				seq1++
				assert.Equal(t, []byte{1}, chunkPayload(m))
			}
		case *message.UpstreamCloseRequest:
			closeSent = true
		}
	}
	assert.Positive(t, seq1, "seq 1 must be resent on the new session")
	assert.True(t, closeSent)
}
