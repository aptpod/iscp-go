package iscp

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/aptpod/iscp-go/v2/encoding/protobuf"
	"github.com/aptpod/iscp-go/v2/log"
	"github.com/aptpod/iscp-go/v2/message"
	"github.com/aptpod/iscp-go/v2/transport"
)

type resumeRetryReadWriter struct {
	onWrite func([]byte) error
}

func (w *resumeRetryReadWriter) Read() ([]byte, error) {
	return nil, transport.EOF
}

func (w *resumeRetryReadWriter) Write(data []byte) error {
	return w.onWrite(data)
}

func (w *resumeRetryReadWriter) Close() error {
	return nil
}

func (w *resumeRetryReadWriter) RxBytesCounterValue() uint64 {
	return 0
}

func (w *resumeRetryReadWriter) TxBytesCounterValue() uint64 {
	return 0
}

func newResumeRetryProtocolSession(responder func(message.Request) (message.Request, error)) *protocolSession {
	var session *protocolSession
	w := &resumeRetryReadWriter{}
	w.onWrite = func(data []byte) error {
		_, msg, err := protobuf.NewEncoding().DecodeFrom(bytes.NewReader(data))
		if err != nil {
			return err
		}
		req, ok := msg.(message.Request)
		if !ok {
			return fmt.Errorf("unexpected message type %T", msg)
		}
		resp, err := responder(req)
		if err != nil {
			return err
		}

		session.mu.Lock()
		reply, ok := session.replyCh[req.GetRequestID()]
		session.mu.Unlock()
		if !ok {
			return fmt.Errorf("reply channel not found for request ID %d", req.GetRequestID())
		}
		reply <- resp
		return nil
	}

	sessionCtx, cancel := context.WithCancel(context.Background())
	session = &protocolSession{
		transport: transport.NewMessageTransport(&transport.MessageTransportConfig{
			Transport: transport.ReadWriter(w),
			Encoding:  protobuf.NewEncoding(),
		}),
		ctx:             sessionCtx,
		cancel:          cancel,
		idGenerator:     newRequestIDGeneratorForClient(),
		replyCh:         make(map[uint32]chan message.Request),
		logger:          log.NewNop(),
		protocolVersion: "v4.0.0",
		upstreams: &clientUpstreams{
			mu:             &sync.RWMutex{},
			acks:           make(map[uint32]chan *message.UpstreamChunkAck),
			aliases:        make(map[uuid.UUID]uint32),
			messageWriters: make(map[uint32]*transport.MessageTransport),
		},
		downstreams: &clientDownstreams{
			mu:            &sync.RWMutex{},
			aliases:       make(map[uuid.UUID]uint32),
			dps:           make(map[uint32]chan *message.DownstreamChunk),
			dpsUnreliable: make(map[uint32]chan *message.DownstreamChunk),
			ackCompletes:  make(map[uint32]chan *message.DownstreamChunkAckComplete),
			metadata:      make(map[uint32]map[string]chan *message.DownstreamMetadata),
		},
	}
	return session
}

func resumeRetrySuccessResponder(req message.Request) (message.Request, error) {
	switch req := req.(type) {
	case *message.DownstreamResumeRequest:
		return &message.DownstreamResumeResponse{
			RequestID:  req.RequestID,
			ResultCode: message.ResultCodeSucceeded,
		}, nil
	case *message.UpstreamResumeRequest:
		return &message.UpstreamResumeResponse{
			RequestID:             req.RequestID,
			ResultCode:            message.ResultCodeSucceeded,
			AssignedStreamIDAlias: 1,
		}, nil
	default:
		return nil, fmt.Errorf("unexpected resume request type %T", req)
	}
}

type cancelOnErrContext struct {
	done chan struct{}
	once sync.Once
}

func newCancelOnErrContext() *cancelOnErrContext {
	return &cancelOnErrContext{done: make(chan struct{})}
}

func (c *cancelOnErrContext) Deadline() (time.Time, bool) {
	return time.Time{}, false
}

func (c *cancelOnErrContext) Done() <-chan struct{} {
	return c.done
}

func (c *cancelOnErrContext) Err() error {
	c.once.Do(func() {
		close(c.done)
	})
	return context.Canceled
}

func (c *cancelOnErrContext) Value(key any) any {
	return nil
}

func newResumeRetryDownstream(ctx context.Context, wireConn *protocolSession) *Downstream {
	d := &Downstream{
		ctx:             ctx,
		cancel:          func() {},
		ID:              uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		Config:          DownstreamConfig{QoS: message.QoSUnreliable},
		wireConn:        wireConn,
		idAlias:         1,
		state:           newStreamState(),
		eventDispatcher: newEventDispatcher(),
		logger:          log.NewNop(),
	}
	d.state.Swap(streamStatusResuming)
	return d
}

func newResumeRetryUpstream(ctx context.Context, wireConn *protocolSession) *Upstream {
	u := &Upstream{
		ctx:             ctx,
		cancel:          func() {},
		ID:              uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		Config:          UpstreamConfig{QoS: message.QoSUnreliable},
		wireConn:        wireConn,
		closeTimeout:    time.Second,
		state:           newStreamState(),
		eventDispatcher: newEventDispatcher(),
		logger:          log.NewNop(),
	}
	u.state.Swap(streamStatusResuming)
	return u
}

func TestDownstreamResumeCanceledBeforeRetryDoesNotConnect(t *testing.T) {
	session := newResumeRetryProtocolSession(resumeRetrySuccessResponder)
	defer session.cancel()

	d := newResumeRetryDownstream(newCancelOnErrContext(), session)
	err := d.resume(&Conn{wireConn: session})

	require.ErrorIs(t, err, context.Canceled)
	require.NotEqual(t, streamStatusConnected, d.state.Current())
}

func TestDownstreamResumeCancelDuringRetryDoesNotWaitForBackoff(t *testing.T) {
	const conflictsBeforeCancel = 6

	conflictReached := make(chan struct{})
	requestCount := 0
	session := newResumeRetryProtocolSession(func(req message.Request) (message.Request, error) {
		resumeReq, ok := req.(*message.DownstreamResumeRequest)
		if !ok {
			return nil, fmt.Errorf("unexpected message type %T", req)
		}
		requestCount++
		if requestCount == conflictsBeforeCancel {
			close(conflictReached)
		}
		return &message.DownstreamResumeResponse{
			RequestID:  resumeReq.RequestID,
			ResultCode: message.ResultCodeResumeRequestConflict,
		}, nil
	})
	defer session.cancel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := newResumeRetryDownstream(ctx, session)
	done := make(chan error, 1)
	go func() {
		done <- d.resume(&Conn{wireConn: session})
	}()

	select {
	case <-conflictReached:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for repeated conflict responses")
	}

	cancelStarted := time.Now()
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
		require.Less(t, time.Since(cancelStarted), time.Second)
		require.NotEqual(t, streamStatusConnected, d.state.Current())
	case <-time.After(time.Second):
		t.Fatal("resume did not stop promptly after context cancellation")
	}
}

func TestUpstreamResumeCanceledBeforeRetryReturnsError(t *testing.T) {
	session := newResumeRetryProtocolSession(resumeRetrySuccessResponder)
	defer session.cancel()

	u := newResumeRetryUpstream(newCancelOnErrContext(), session)
	err := u.resume(session)

	require.ErrorIs(t, err, context.Canceled)
	require.NotEqual(t, streamStatusConnected, u.state.Current())
}
