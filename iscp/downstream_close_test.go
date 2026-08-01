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

type downstreamCloseReadWriter struct {
	onWrite       func([]byte) error
	responseDelay time.Duration
}

func (w *downstreamCloseReadWriter) Read() ([]byte, error) {
	return nil, transport.EOF
}

func (w *downstreamCloseReadWriter) Write(data []byte) error {
	return w.onWrite(data)
}

func (w *downstreamCloseReadWriter) Close() error {
	return nil
}

func (w *downstreamCloseReadWriter) RxBytesCounterValue() uint64 {
	return 0
}

func (w *downstreamCloseReadWriter) TxBytesCounterValue() uint64 {
	return 0
}

func newDownstreamCloseProtocolSession(w *downstreamCloseReadWriter) *protocolSession {
	var session *protocolSession
	w.onWrite = func(data []byte) error {
		_, msg, err := protobuf.NewEncoding().DecodeFrom(bytes.NewReader(data))
		if err != nil {
			return err
		}
		req, ok := msg.(*message.DownstreamCloseRequest)
		if !ok {
			return fmt.Errorf("unexpected message type %T", msg)
		}

		respond := func() {
			session.mu.Lock()
			reply := session.replyCh[uint32(req.RequestID)]
			session.mu.Unlock()
			reply <- &message.DownstreamCloseResponse{
				RequestID:    req.RequestID,
				ResultCode:   message.ResultCodeSucceeded,
				ResultString: "OK",
			}
		}
		if w.responseDelay > 0 {
			go func() {
				time.Sleep(w.responseDelay)
				respond()
			}()
		} else {
			respond()
		}
		return nil
	}

	session = &protocolSession{
		transport: transport.NewMessageTransport(&transport.MessageTransportConfig{
			Transport: transport.ReadWriter(w),
			Encoding:  protobuf.NewEncoding(),
		}),
		ctx:         context.Background(),
		idGenerator: newRequestIDGeneratorForClient(),
		replyCh:     make(map[uint32]chan message.Request),
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

func TestDownstreamCloseUsesTimeoutForFinalAck(t *testing.T) {
	const closeTimeout = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &downstreamCloseReadWriter{responseDelay: closeTimeout * 2}
	d := &Downstream{
		ctx:             ctx,
		cancel:          cancel,
		ID:              uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		wireConn:        newDownstreamCloseProtocolSession(w),
		closeTimeout:    closeTimeout,
		finalAckFlushed: make(chan struct{}),
		state:           newStreamState(),
		eventDispatcher: newEventDispatcher(),
		logger:          log.NewNop(),
	}

	started := time.Now()
	done := make(chan error, 1)
	go func() {
		done <- d.Close(context.Background())
	}()

	select {
	case err := <-done:
		require.NoError(t, err)
		require.GreaterOrEqual(t, time.Since(started), closeTimeout)
	case <-time.After(100 * time.Millisecond):
		cancel()
		require.NoError(t, <-done)
		t.Fatal("Close did not return after the final ack wait deadline")
	}
}
