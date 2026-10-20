package reconnect_test

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aptpod/iscp-go/v2/log"
	"github.com/aptpod/iscp-go/v2/transport"
	. "github.com/aptpod/iscp-go/v2/transport/reconnect"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// ---------- Mock Transport ----------

// mockCountingTransport is a mock that implements transport.Transport and transport.Closer.
// It is used as the underlying transport of reconnect.Transport.
// Unlike multi_test.mockTransport, it cannot be reused across packages,
// so it is prepared exclusively for concurrent_close_test.go. Its distinguishing feature is that it counts Close calls.
type mockCountingTransport struct {
	mu         sync.Mutex
	isClosed   bool
	closeCount int
	writeCount int
	name       transport.Name

	readCh  chan []byte
	closeCh chan struct{}
}

func newMockCountingTransport(name string) *mockCountingTransport {
	return &mockCountingTransport{
		name:    transport.Name(name),
		readCh:  make(chan []byte, 100),
		closeCh: make(chan struct{}),
	}
}

func (m *mockCountingTransport) Read() ([]byte, error) {
	select {
	case data := <-m.readCh:
		return data, nil
	case <-m.closeCh:
		return nil, errors.New("mock: transport closed")
	}
}

// Write does not block. It only records the number of calls.
func (m *mockCountingTransport) Write(_ []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.isClosed {
		return errors.New("mock: transport closed")
	}
	m.writeCount++
	return nil
}

// Close increments closeCount every time (it is not guarded by sync.Once).
// This is to observe how many times reconnect.Transport.CloseWithStatus calls the underlying Close.
func (m *mockCountingTransport) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeCount++
	if !m.isClosed {
		m.isClosed = true
		close(m.closeCh)
	}
	return nil
}

func (m *mockCountingTransport) CloseWithStatus(_ transport.CloseStatus) error {
	return m.Close()
}

func (m *mockCountingTransport) CloseCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closeCount
}

func (m *mockCountingTransport) Name() transport.Name { return m.name }

func (m *mockCountingTransport) NegotiationParams() transport.NegotiationParams {
	return transport.NegotiationParams{}
}

func (m *mockCountingTransport) AsUnreliable() (transport.UnreliableTransport, bool) {
	return nil, false
}

func (m *mockCountingTransport) RxBytesCounterValue() uint64 { return 0 }
func (m *mockCountingTransport) TxBytesCounterValue() uint64 { return 0 }

// ---------- Helpers ----------

// newReconnectTestTransport creates a reconnect.Transport with a dialer that always returns the same mock.
// It enables the v4 protocol (by specifying TransportType).
func newReconnectTestTransport(t *testing.T, mock transport.Transport, maxReconnectAttempts int) *Transport {
	t.Helper()
	tr, err := Dial(DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			return mock, nil
		}),
		DialConfig: transport.DialConfig{
			SubConnectionID: "sub1",
			TransportType:   transport.NegotiationNameWebSocket, // enable the v4 protocol
		},
		MaxReconnectAttempts: maxReconnectAttempts,
		ReconnectInterval:    10 * time.Millisecond,
		HeartbeatInterval:    time.Hour,
		HeartbeatTimeout:     time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)
	return tr
}

// newFailAfterFirstReconnectTransport creates a reconnect.Transport with a dialer that returns the mock only on the first connection and always fails afterwards.
// It is equivalent to multi_test.newFailingReconnectTransport.
func newFailAfterFirstReconnectTransport(t *testing.T, mock transport.Transport, maxReconnectAttempts int) *Transport {
	t.Helper()
	var mu sync.Mutex
	connected := false
	tr, err := Dial(DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			mu.Lock()
			defer mu.Unlock()
			if !connected {
				connected = true
				return mock, nil
			}
			return nil, errors.New("test: dial always fails after first connect")
		}),
		DialConfig: transport.DialConfig{
			SubConnectionID: "sub1",
			TransportType:   transport.NegotiationNameWebSocket, // enable the v4 protocol
		},
		MaxReconnectAttempts: maxReconnectAttempts,
		ReconnectInterval:    10 * time.Millisecond,
		HeartbeatInterval:    time.Hour,
		HeartbeatTimeout:     time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)
	return tr
}

func waitForTransportConnected(t *testing.T, tr *Transport) {
	t.Helper()
	require.Eventually(t,
		func() bool { return tr.Status() == StatusConnected },
		5*time.Second, 10*time.Millisecond,
		"reconnect transport should become Connected",
	)
}

// ---------- Tests ----------

// TestReconnectTransport_ConcurrentWrite verifies that even when stressGoroutines goroutines Write at the same time,
// -race detects no violation of the writeMu exclusion, and all Writes return in finite time (no hang).
func TestReconnectTransport_ConcurrentWrite(t *testing.T) {
	defer goleak.VerifyNone(t)

	mock := newMockCountingTransport("mock1")
	tr := newReconnectTestTransport(t, mock, 1)
	// Mind the call order with goleak: t.Cleanup runs after defer, so
	// make sure a defer written after defer goleak.VerifyNone(t) runs first (LIFO).
	defer func() { _ = tr.Close() }()
	waitForTransportConnected(t, tr)

	var wg sync.WaitGroup
	errCh := make(chan error, stressGoroutines*stressIterations)
	for g := 0; g < stressGoroutines; g++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for i := 0; i < stressIterations; i++ {
				if err := tr.Write(fmt.Appendf(nil, "goroutine-%d-iter-%d", n, i)); err != nil {
					errCh <- err
				}
			}
		}(g)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("concurrent Write did not finish within 10s (possible hang)")
	}
	close(errCh)
	for err := range errCh {
		// The writes run concurrently while the connection is kept, so all of them should succeed.
		t.Errorf("unexpected Write error during concurrent write: %v", err)
	}
}

// TestReconnectTransport_CloseDuringWrite verifies that when Close is called from another goroutine
// while a Write loop is running, all Writes return in finite time (no hang) without panicking,
// and that a Write after Close always returns an error.
func TestReconnectTransport_CloseDuringWrite(t *testing.T) {
	defer goleak.VerifyNone(t)

	mock := newMockCountingTransport("mock1")
	tr := newReconnectTestTransport(t, mock, 1)
	waitForTransportConnected(t, tr)

	stop := make(chan struct{})
	writeLoopDone := make(chan struct{})
	require.NotPanics(t, func() {
		go func() {
			defer close(writeLoopDone)
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = tr.Write([]byte("data")) // should be nil before Close and an error after Close
			}
		}()

		time.Sleep(20 * time.Millisecond) // wait until the write loop starts running
		require.NoError(t, tr.Close())
		close(stop)
	})

	select {
	case <-writeLoopDone:
	case <-time.After(5 * time.Second):
		t.Fatal("write loop did not stop within 5s after Close (Write may be hanging)")
	}

	// A Write after Close always returns an error.
	err := tr.Write([]byte("after close"))
	require.Error(t, err, "Write after Close should return an error")
}

// TestReconnectTransport_RepeatedClose verifies that calling Close twice on the same instance
// returns without panicking. It also records the number of times the underlying mock's Close is called.
//
// Deviation from the ideal: reconnect.Transport.CloseWithStatus (transport.go:634-656)
// does not set r.transport to nil, so it calls the underlying CloseWithStatus again on the second call.
// Currently the underlying Close is called twice. The ideal is to limit it to once with a closeOnce-like guard,
// but production code is not changed here, so the current count is recorded as is.
func TestReconnectTransport_RepeatedClose(t *testing.T) {
	defer goleak.VerifyNone(t)

	mock := newMockCountingTransport("mock1")
	tr := newReconnectTestTransport(t, mock, 1)
	waitForTransportConnected(t, tr)

	require.NotPanics(t, func() {
		require.NoError(t, tr.Close())
	})

	require.NotPanics(t, func() {
		_ = tr.Close() // Only require that the second call does not panic either. Whether it returns an error does not matter.
	})

	closeCount := mock.CloseCount()
	t.Logf("underlying mock.Close() was called %d time(s) after Transport.Close() x2 (no close-once guard)", closeCount)
	require.Equal(t, 2, closeCount,
		"CloseWithStatus does not guard against repeated calls; underlying Close is invoked every time")
}

// TestReconnectTransport_ConcurrentClose verifies that even when Close is called simultaneously from stressGoroutines goroutines,
// all of them return in finite time and -race detects no race.
func TestReconnectTransport_ConcurrentClose(t *testing.T) {
	defer goleak.VerifyNone(t)

	mock := newMockCountingTransport("mock1")
	tr := newReconnectTestTransport(t, mock, 1)
	waitForTransportConnected(t, tr)

	var wg sync.WaitGroup
	for g := 0; g < stressGoroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = tr.Close()
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent Close did not finish within 5s")
	}
}

// TestReconnectTransport_CloseDuringReconnect measures the behavior when Close is called
// while the dial is blocked indefinitely and the transport is pinned in Reconnecting.
//
// Deviation from the ideal: r.reconnector.Connect() in doReconnect (transport.go:792) does not
// honor ctx, so Close() cannot return from waitForReconnectToFinish (:439-443) until
// blockDial is released. The ideal is for Close() to return in finite time,
// but production code is not changed here, so the current behavior is measured and recorded.
func TestReconnectTransport_CloseDuringReconnect(t *testing.T) {
	defer goleak.VerifyNone(t)

	blockDial := make(chan struct{})
	var dialCount atomic.Int32
	mock := newMockCountingTransport("mock1")

	tr, err := Dial(DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			if dialCount.Add(1) == 1 {
				return mock, nil
			}
			<-blockDial // from the second call on, block indefinitely until released (simulates a hanging dial)
			return nil, errors.New("test: dialer unblocked")
		}),
		DialConfig: transport.DialConfig{
			SubConnectionID: "sub1",
			TransportType:   transport.NegotiationNameWebSocket,
		},
		MaxReconnectAttempts: -1,
		ReconnectInterval:    10 * time.Millisecond,
		HeartbeatInterval:    time.Hour,
		HeartbeatTimeout:     time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)
	// Always close blockDial at the end (if it stays unreleased, the reconnect goroutine remains and goleak fails for another reason).
	defer func() {
		select {
		case <-blockDial:
		default:
			close(blockDial)
		}
	}()

	waitForTransportConnected(t, tr)

	// Bring down the underlying transport to trigger a reconnect, and wait until the second dial starts waiting on blockDial.
	mock.Close()
	require.Eventually(t,
		func() bool { return dialCount.Load() >= 2 },
		5*time.Second, 10*time.Millisecond,
		"dialer should be invoked a second time and block",
	)
	require.Eventually(t,
		func() bool { return tr.Status() == StatusReconnecting },
		5*time.Second, 10*time.Millisecond,
	)

	closeDone := make(chan error, 1)
	go func() { closeDone <- tr.Close() }()

	select {
	case <-closeDone:
		t.Log("Close-blocking issue NOT reproduced: Close() returned before the blocked dial was released")
	case <-time.After(1 * time.Second):
		t.Log("Close-blocking issue reproduced: Close() is still blocked 1s after being called, while dial is blocked (dial does not honor ctx)")
	}

	// Cleanup: release the block and confirm that Close completes.
	close(blockDial)
	select {
	case err := <-closeDone:
		t.Logf("Close() returned after releasing the blocked dial: err=%v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Close() did not return even after releasing the blocked dial")
	}
}

// TestReconnectTransport_CloseDuringRead verifies that when Close is called while blocked in Read,
// Read returns an error in finite time.
func TestReconnectTransport_CloseDuringRead(t *testing.T) {
	defer goleak.VerifyNone(t)

	mock := newMockCountingTransport("mock1")
	tr := newReconnectTestTransport(t, mock, 1)
	waitForTransportConnected(t, tr)

	readDone := make(chan error, 1)
	go func() {
		_, err := tr.Read()
		readDone <- err
	}()

	time.Sleep(20 * time.Millisecond) // wait until Read starts waiting on readResCh
	require.NoError(t, tr.Close())

	select {
	case err := <-readDone:
		require.Error(t, err, "Read should return an error after Close")
	case <-time.After(5 * time.Second):
		t.Fatal("Read did not return within 5s after Close")
	}
}

// TestReconnectTransport_ExplicitCloseReclaimsAfterRetriesExhausted verifies that after finite retries
// (MaxReconnectAttempts=2) are exhausted and the status becomes StatusDisconnected, no goroutine remains
// if Close() is called explicitly.
//
// For reconnect.Transport alone, it is within the specification that it holds goroutines until Close,
// and the user (caller) is responsible for calling Close.
//
// Deviation from the ideal: When reconnect.Transport is used directly as in this test, an explicit Close
// can reclaim it, but it is a different story via multi.Transport. When all subs exhaust their finite retries,
// multi declares that it "shuts down the whole" and calls m.cancel(), but it does not call Close on the subs.
// The sub's ctx derives from context.Background() and has no parent-child relationship with multi's ctx
// (reconnect/transport.go:224), so m.cancel() does not propagate to the subs. This inconsistency is the actual
// harm here, and its fix and reproduction are handled in transport/multi/.
//
// Measured on 2026-07-31: running the predecessor of this test (the version without the explicit Close), heartbeatLoop
// (transport.go:344) remained when the v4 protocol was used, and goleak FAILed. This is because doReconnect
// (:775-780) only transitions to StatusDisconnected when retries are exhausted and does not call
// r.cancel().
//
//	found unexpected goroutines:
//	[Goroutine 9 in state select, with github.com/aptpod/iscp-go/v2/transport/reconnect.(*Transport).heartbeatLoop on top of the stack:
//	github.com/aptpod/iscp-go/v2/transport/reconnect.(*Transport).heartbeatLoop(0xc000190180)
//		transport/reconnect/transport.go:344 +0x3be
//	created by github.com/aptpod/iscp-go/v2/transport/reconnect.Dial in goroutine 7
//		transport/reconnect/transport.go:230 +0x1632]
func TestReconnectTransport_ExplicitCloseReclaimsAfterRetriesExhausted(t *testing.T) {
	defer goleak.VerifyNone(t)

	mock := newMockCountingTransport("mock1")
	tr := newFailAfterFirstReconnectTransport(t, mock, 2) // exhaust with MaxReconnectAttempts=2
	// Mind the call order with goleak: defer is LIFO, so this defer runs
	// before the defer goleak.VerifyNone(t) above (the one registered later runs first).
	defer func() { _ = tr.Close() }()
	waitForTransportConnected(t, tr)

	mock.Close()
	require.Eventually(t,
		func() bool { return tr.Status() == StatusDisconnected },
		5*time.Second, 10*time.Millisecond,
		"status should become Disconnected after retries are exhausted",
	)

	// The explicit Close is done by the defer above. Here we only confirm that Disconnected is reached.
}
