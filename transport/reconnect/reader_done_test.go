package reconnect_test

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/aptpod/iscp-go/v2/log"
	"github.com/aptpod/iscp-go/v2/transport"
	. "github.com/aptpod/iscp-go/v2/transport/reconnect"
)

// gatedCloseTransport is a wrapper that gates Close of mockCountingTransport.
// The first Close notifies entered and blocks until release. It is used to deliberately
// prolong old.Close() in doReconnect and queue read results onto the old transport meanwhile.
type gatedCloseTransport struct {
	*mockCountingTransport
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (g *gatedCloseTransport) Close() error {
	g.once.Do(func() {
		close(g.entered)
		<-g.release
	})
	return g.mockCountingTransport.Close()
}

func (g *gatedCloseTransport) CloseWithStatus(_ transport.CloseStatus) error {
	return g.Close()
}

func iscpMsg(payload ...byte) []byte {
	return append([]byte{byte(MessageTypeISCP)}, payload...)
}

// TestReconnectTransport_ReadLoopDoesNotStallOnStaleUnconsumedReads verifies that, while readLoop waits for
// the reader goroutine to exit, the unconsumed read results left in readCh are drained.
//
// Trigger condition: after processReads enters reconnect through the heartbeat timeout (timerC) path,
// nobody receives from readCh. If the reader produces two results in the meantime, the first fills
// readCh (cap 1) and the reader blocks sending the second. This send is not released by tr.Close()
// (because it is not in Read) and r.ctx is still alive, so even if the reconnect succeeds, readLoop
// stops forever waiting for the reader to exit (data from the new transport never flows again).
// It is hit by a realistic sequence in which the heartbeat timeout fires on a network stall and the
// backlogged data then arrives in a burst.
//
// The test stalls old.Close() in doReconnect with a gate, and meanwhile delivers two pieces of data
// to the old transport to deterministically create "1 buffered + 1 blocked send".
// The oracle is that the data from the new transport after the reconnect can be obtained
// from the public Read.
func TestReconnectTransport_ReadLoopDoesNotStallOnStaleUnconsumedReads(t *testing.T) {
	defer goleak.VerifyNone(t)

	mock1 := &gatedCloseTransport{
		mockCountingTransport: newMockCountingTransport("mock1"),
		entered:               make(chan struct{}),
		release:               make(chan struct{}),
	}
	mock2 := newMockCountingTransport("mock2")

	var mu sync.Mutex
	dialCount := 0
	tr, err := Dial(DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			mu.Lock()
			defer mu.Unlock()
			dialCount++
			if dialCount == 1 {
				return mock1, nil
			}
			return mock2, nil
		}),
		DialConfig: transport.DialConfig{
			SubConnectionID: "sub1",
			TransportType:   transport.NegotiationNameWebSocket, // v4 (with heartbeat timeout)
		},
		MaxReconnectAttempts: -1,
		ReconnectInterval:    10 * time.Millisecond,
		HeartbeatInterval:    time.Hour,
		HeartbeatTimeout:     300 * time.Millisecond,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)

	waitForTransportConnected(t, tr)

	// Without sending data, wait for heartbeat timeout -> reconnect -> reaching the gate in old.Close().
	select {
	case <-mock1.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("heartbeat timeout did not trigger reconnect")
	}

	// While old.Close() is blocked, deliver two pieces of data to the old transport.
	// Wait for the reader to consume both (the first fills readCh and the second
	// blocks on send) before releasing the gate.
	mock1.readCh <- iscpMsg(1)
	mock1.readCh <- iscpMsg(2)
	require.Eventually(t,
		func() bool { return len(mock1.readCh) == 0 },
		5*time.Second, time.Millisecond,
		"reader should consume both pending reads",
	)
	close(mock1.release)

	// After the reconnect completes, the data from the new transport must be obtainable from the public Read.
	// If readLoop is stuck waiting for the old reader to exit, this does not progress.
	waitForTransportConnected(t, tr)
	mock2.readCh <- iscpMsg(3)

	readDone := make(chan []byte, 1)
	go func() {
		if bs, err := tr.Read(); err == nil {
			readDone <- bs
		}
	}()
	select {
	case bs := <-readDone:
		require.Equal(t, []byte{3}, bs)
	case <-time.After(3 * time.Second):
		t.Fatal("data from the new transport was not delivered: readLoop is stuck waiting for the old reader")
	}

	require.NoError(t, tr.Close())
}
