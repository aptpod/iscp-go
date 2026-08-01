package iscp

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/aptpod/iscp-go/v2/message"
	"github.com/aptpod/iscp-go/v2/transport"
)

type downstreamAckEncoding struct {
	started chan *message.DownstreamChunkAck
	release chan struct{}
}

func (e *downstreamAckEncoding) EncodeTo(_ io.Writer, msg message.Message) (int, error) {
	ack, ok := msg.(*message.DownstreamChunkAck)
	if !ok {
		return 0, fmt.Errorf("unexpected message type %T", msg)
	}
	e.started <- ack
	<-e.release
	return 0, nil
}

func (e *downstreamAckEncoding) DecodeFrom(io.Reader) (int, message.Message, error) {
	return 0, nil, fmt.Errorf("DecodeFrom is not implemented")
}

func (e *downstreamAckEncoding) ContentType() transport.ContentType {
	return transport.ContentTypeBinary
}

func (e *downstreamAckEncoding) Name() transport.EncodingName {
	return transport.EncodingName("downstream-ack-test")
}

func TestDownstreamFlushAckReleasesMutexBeforeWrite(t *testing.T) {
	encoding := &downstreamAckEncoding{
		started: make(chan *message.DownstreamChunkAck),
		release: make(chan struct{}),
	}
	w := &downstreamCloseReadWriter{onWrite: func([]byte) error { return nil }}
	session := &protocolSession{
		transport: transport.NewMessageTransport(&transport.MessageTransportConfig{
			Transport: transport.ReadWriter(w),
			Encoding:  encoding,
		}),
	}
	d := &Downstream{
		ctx:                   context.Background(),
		wireConn:              session,
		idAlias:               1,
		chunkAckIDSequence:    newSequenceNumberGenerator(0),
		upstreamInfoAckBuffer: make(map[uint32]*message.UpstreamInfo),
		dataIDAckBuffer:       make(map[uint32]*message.DataID),
		resultAckBuffer:       make([]*message.DownstreamChunkResult, 0, 1),
	}

	first := &message.DownstreamChunkResult{ResultString: "first"}
	second := &message.DownstreamChunkResult{ResultString: "second"}
	d.pushResultAckBuffer(first)

	flushDone := make(chan error, 1)
	go func() {
		flushDone <- d.flushAck()
	}()
	ack := <-encoding.started

	pushDone := make(chan struct{})
	go func() {
		d.pushResultAckBuffer(second)
		close(pushDone)
	}()

	released := false
	release := func() {
		if !released {
			close(encoding.release)
			released = true
		}
	}
	defer release()

	select {
	case <-pushDone:
		require.Same(t, first, ack.Results[0])
	case <-time.After(100 * time.Millisecond):
		release()
		require.NoError(t, <-flushDone)
		t.Fatal("pushResultAckBuffer blocked while ack was being written")
	}

	release()
	require.NoError(t, <-flushDone)
}
