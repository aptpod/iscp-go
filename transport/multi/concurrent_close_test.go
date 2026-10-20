package multi_test

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aptpod/iscp-go/v2/log"
	"github.com/aptpod/iscp-go/v2/transport"
	. "github.com/aptpod/iscp-go/v2/transport/multi"
	"github.com/aptpod/iscp-go/v2/transport/reconnect"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// ---------- Helpers ----------

// countingMockTransport is equivalent to mockTransport (transport_test.go), but it can record the number of
// Close calls. It is prepared only for concurrent_close_test.go to observe that
// multi.Transport.CloseWithStatus (transport.go:402-417) has no guard.
// newTestReconnectTransport cannot be used because its argument type is fixed to *mockTransport,
// so newReconnectTransportWithMock is provided separately.
type countingMockTransport struct {
	mu         sync.Mutex
	isClosed   bool
	closeCount int
	name       transport.Name

	readCh  chan []byte
	closeCh chan struct{}
}

func newCountingMockTransport(name string) *countingMockTransport {
	return &countingMockTransport{
		name:    transport.Name(name),
		readCh:  make(chan []byte, 100),
		closeCh: make(chan struct{}),
	}
}

func (m *countingMockTransport) Read() ([]byte, error) {
	select {
	case data := <-m.readCh:
		return data, nil
	case <-m.closeCh:
		return nil, errors.New("mock: transport closed")
	}
}

func (m *countingMockTransport) Write(_ []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.isClosed {
		return errors.New("mock: transport closed")
	}
	return nil
}

func (m *countingMockTransport) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeCount++
	if !m.isClosed {
		m.isClosed = true
		close(m.closeCh)
	}
	return nil
}

func (m *countingMockTransport) CloseWithStatus(_ transport.CloseStatus) error {
	return m.Close()
}

func (m *countingMockTransport) CloseCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closeCount
}

func (m *countingMockTransport) Name() transport.Name { return m.name }

func (m *countingMockTransport) NegotiationParams() transport.NegotiationParams {
	return transport.NegotiationParams{}
}

func (m *countingMockTransport) AsUnreliable() (transport.UnreliableTransport, bool) {
	return nil, false
}

func (m *countingMockTransport) RxBytesCounterValue() uint64 { return 0 }
func (m *countingMockTransport) TxBytesCounterValue() uint64 { return 0 }

// newReconnectTransportWithMock creates a reconnect.Transport with a fixed dialer that returns the specified transport.Transport.
// Use it when a different mock type such as countingMockTransport cannot be passed to newTestReconnectTransport,
// whose argument type is fixed to *mockTransport.
func newReconnectTransportWithMock(t *testing.T, mock transport.Transport, subConnID string) *reconnect.Transport {
	t.Helper()
	rt, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			return mock, nil
		}),
		DialConfig: transport.DialConfig{
			SubConnectionID:   transport.SubConnectionID(subConnID),
			SuperConnectionID: transport.SuperConnectionID(testSuperConnectionID),
			TransportType:     transport.NegotiationNameWebSocket,
		},
		MaxReconnectAttempts: 1,
		ReconnectInterval:    10 * time.Millisecond,
		HeartbeatInterval:    time.Hour,
		HeartbeatTimeout:     time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)
	return rt
}

// newReopenableReconnectTransport creates a reconnect.Transport with a dialer that generates a new mockTransport on every dial.
// Retries are unlimited (-1), so closing the underlying mock makes it
// Reconnecting, and it returns to Connected with a new mock; this round trip can be repeated.
// The returned drop function closes the current underlying mock.
func newReopenableReconnectTransport(t *testing.T, subConnID string) (rt *reconnect.Transport, drop func()) {
	t.Helper()
	var mu sync.Mutex
	var current *mockTransport
	rt, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			mu.Lock()
			current = newMockTransport(subConnID)
			m := current
			mu.Unlock()
			return m, nil
		}),
		DialConfig: transport.DialConfig{
			SubConnectionID:   transport.SubConnectionID(subConnID),
			SuperConnectionID: transport.SuperConnectionID(testSuperConnectionID),
			TransportType:     transport.NegotiationNameWebSocket,
		},
		MaxReconnectAttempts: -1,
		ReconnectInterval:    2 * time.Millisecond,
		HeartbeatInterval:    time.Hour,
		HeartbeatTimeout:     time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)
	drop = func() {
		mu.Lock()
		m := current
		mu.Unlock()
		if m != nil {
			m.Close()
		}
	}
	return rt, drop
}

// blockingWriteMockTransport is a mock whose Write / Read keep blocking until unblock() is called.
// It is only for verifying that a Write blocked in the underlying transport does not fall back to another sub.
type blockingWriteMockTransport struct {
	name      transport.Name
	unblockCh chan struct{}
	once      sync.Once
}

func newBlockingWriteMockTransport(name string) *blockingWriteMockTransport {
	return &blockingWriteMockTransport{
		name:      transport.Name(name),
		unblockCh: make(chan struct{}),
	}
}

func (m *blockingWriteMockTransport) Read() ([]byte, error) {
	<-m.unblockCh
	return nil, errors.New("mock: unblocked")
}

func (m *blockingWriteMockTransport) Write(_ []byte) error {
	<-m.unblockCh
	return errors.New("mock: unblocked")
}

func (m *blockingWriteMockTransport) Close() error {
	m.unblock()
	return nil
}

func (m *blockingWriteMockTransport) CloseWithStatus(_ transport.CloseStatus) error {
	return m.Close()
}

func (m *blockingWriteMockTransport) unblock() {
	m.once.Do(func() { close(m.unblockCh) })
}

func (m *blockingWriteMockTransport) Name() transport.Name { return m.name }

func (m *blockingWriteMockTransport) NegotiationParams() transport.NegotiationParams {
	return transport.NegotiationParams{}
}

func (m *blockingWriteMockTransport) AsUnreliable() (transport.UnreliableTransport, bool) {
	return nil, false
}

func (m *blockingWriteMockTransport) RxBytesCounterValue() uint64 { return 0 }
func (m *blockingWriteMockTransport) TxBytesCounterValue() uint64 { return 0 }

// ---------- Tests ----------

// TestMultiTransport_ConcurrentWriteAndClose verifies that even when another goroutine
// calls Close while stressGoroutines goroutines are in Write, all Writes return in finite time (no hang) and -race is clean.
//
// It uses countingMockTransport instead of the existing mockTransport (transport_test.go).
// mockTransport.Write only writes to the internal writeCh (buffer 100) and nobody reads from it, so
// performing stressGoroutines x stressIterations Writes (32 x 200 = 6400 in the stress build)
// certainly exhausts the buffer and Write stays blocked. A deadlock that times out after
// 15 minutes was actually confirmed in the stress build (mockTransport.Write blocks on
// the send to writeCh, and other Write / Close calls keep waiting for the internal
// mutex of reconnect.Transport that it holds). countingMockTransport.Write buffers
// nothing and succeeds immediately, so this problem does not occur.
//
// Measured on 2026-07-31 in review: time.Sleep(1ms) was used to target "while Write is running",
// but countingMockTransport.Write is non-blocking and the total number of Writes in the normal build
// (stressGoroutines x stressIterations = 4 x 5 = 20) completes in under 1ms,
// so in the normal build Close always ran after all Writes had finished, which was a false green
// (measured writesBeforeClose=20 writesAfterClose=0). Synchronize by progress instead of time:
// each goroutine notifies once it finishes its first Write, and Close is called after receiving
// stressGoroutines notifications, which reliably creates a state where the remaining iterations are still running.
func TestMultiTransport_ConcurrentWriteAndClose(t *testing.T) {
	defer goleak.VerifyNone(t)

	mock1 := newCountingMockTransport("mock1")
	rt1 := newReconnectTransportWithMock(t, mock1, "sub1")
	waitForConnected(t, rt1)

	id1 := transport.SubConnectionID("transport1")
	selector := newFixedTransportSelector(id1)

	mt, err := NewTransport(TransportConfig{
		TransportMap:      TransportMap{id1: rt1},
		TransportSelector: selector,
		Logger:            log.NewNop(),
	})
	require.NoError(t, err)
	// Note the call order with goleak: defers are LIFO, so this defer
	// runs before the defer goleak.VerifyNone(t) above (the one registered later runs first).
	defer closeMultiAndWait(mt)

	var closeStarted atomic.Bool
	var writesBeforeClose, writesAfterClose atomic.Int64

	var wg sync.WaitGroup
	firstWriteDone := make(chan struct{}, stressGoroutines)
	releaseGate := make(chan struct{})
	for g := 0; g < stressGoroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < stressIterations; i++ {
				if i == 1 {
					// Align all goroutines until they have finished their first Write,
					// then resume the remaining iterations all at once, at almost the same time as the close of releaseGate.
					<-releaseGate
				}
				_ = mt.Write([]byte("data")) // Can be before or after Close. Whether there is an error does not matter.
				if closeStarted.Load() {
					writesAfterClose.Add(1)
				} else {
					writesBeforeClose.Add(1)
				}
				if i == 0 {
					firstWriteDone <- struct{}{}
				}
			}
		}()
	}

	// Wait until all goroutines have finished their first Write, then start Close and
	// resume the remaining iterations all at once at almost the same time. This reliably
	// creates a state where many Writes are in progress at the moment Close is called.
	for g := 0; g < stressGoroutines; g++ {
		<-firstWriteDone
	}
	closeStarted.Store(true)
	go func() { _ = mt.Close() }()
	close(releaseGate)

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

	before, after := writesBeforeClose.Load(), writesAfterClose.Load()
	t.Logf("writesBeforeClose=%d writesAfterClose=%d total=%d", before, after, before+after)
	if after == 0 {
		t.Fatal("writesAfterClose=0: all Writes finished before Close, " +
			"so the situation \"Close during Write\" could not be reproduced")
	}
}

// TestMultiTransport_MultipleCloseCalls verifies that calling multi.Transport.Close twice
// returns without panicking. It also records the number of underlying Close calls of each sub.
//
// Known gap from the ideal: multi.Transport.CloseWithStatus (transport.go:402-417) has no
// guard equivalent to closeOnce, so every call calls CloseWithStatus of all subs again.
// Furthermore, reconnect.Transport has no guard either, so calling multi.Close()
// N times also calls the underlying mock.Close() N times. This test does not
// change production code, so it records the current counts as they are.
//
// When a fix was made on 2026-07-31, a path (via teardownOnce, transport.go:330-333) that runs closeAll
// asynchronously just once when all subs become Disconnected was added, and
// when each sub transitioned to Disconnected on the first mt.Close(), this path fired
// by mistake, and the expected value temporarily went from 2 to 3. This was an unintended re-entry of Close,
// so it was fixed by consuming teardownOnce at the start of CloseWithStatus
// (transport.go:405-410). This expected value of 2 is the regression detection point for
// "the teardown path does not fire on an explicit Close".
func TestMultiTransport_MultipleCloseCalls(t *testing.T) {
	defer goleak.VerifyNone(t)

	mock1 := newCountingMockTransport("mock1")
	mock2 := newCountingMockTransport("mock2")
	rt1 := newReconnectTransportWithMock(t, mock1, "sub1")
	rt2 := newReconnectTransportWithMock(t, mock2, "sub2")
	waitForConnected(t, rt1)
	waitForConnected(t, rt2)

	id1 := transport.SubConnectionID("transport1")
	id2 := transport.SubConnectionID("transport2")

	mt, err := NewTransport(TransportConfig{
		TransportMap:      TransportMap{id1: rt1, id2: rt2},
		TransportSelector: newFixedTransportSelector(id1),
		Logger:            log.NewNop(),
	})
	require.NoError(t, err)

	require.NotPanics(t, func() {
		require.NoError(t, mt.Close())
	})
	require.NotPanics(t, func() {
		_ = mt.Close() // Only require that the second call does not panic either. Whether there is an error does not matter.
	})

	time.Sleep(200 * time.Millisecond) // wait for the goroutine cleanup

	c1, c2 := mock1.CloseCount(), mock2.CloseCount()
	t.Logf("after multi.Close() x2, underlying Close was called mock1=%d mock2=%d times (no close-once guard in multi nor reconnect; teardownOnce is consumed by CloseWithStatus so closeAll does not re-enter)", c1, c2)
	require.Equal(t, 2, c1, "number of underlying Close calls of sub1 (explicit Close x2; closeAll does not re-enter because teardownOnce is consumed)")
	require.Equal(t, 2, c2, "number of underlying Close calls of sub2 (explicit Close x2; closeAll does not re-enter because teardownOnce is consumed)")
}

// TestMultiTransport_BlockedWriteIsReleasedByClose directly verifies the requirement that a Write blocked
// is released by Close. With all subs fixed to Connecting (newAlwaysFailingConnectingTransport) and
// NoConnectedTransportTimeout=0 (no giving up), starting from a state where Write is blocked,
// it confirms that Close from another goroutine makes Write return an error in finite time, repeating
// stressIterationsSlow times.
//
// Each iteration includes a fixed time.Sleep (about 250ms measured, including the NewTransport creation cost), so
// using stressIterations (200 in the stress build) as is would take about 50 seconds by simple calculation.
// It is real-time dependent, so stressIterationsSlow is used (see stress_params_*.go).
func TestMultiTransport_BlockedWriteIsReleasedByClose(t *testing.T) {
	defer goleak.VerifyNone(t)

	for i := 0; i < stressIterationsSlow; i++ {
		if err := writeBlockedByCloseRound(t, i); err != nil {
			t.Fatalf("iteration %d failed: %v", i, err)
		}
	}
}

func writeBlockedByCloseRound(t *testing.T, iter int) error {
	t.Helper()

	rt1 := newAlwaysFailingConnectingTransport(t, "sub1")
	rt2 := newAlwaysFailingConnectingTransport(t, "sub2")

	id1 := transport.SubConnectionID("transport1")
	id2 := transport.SubConnectionID("transport2")

	mt, err := NewTransport(TransportConfig{
		TransportMap:      TransportMap{id1: rt1, id2: rt2},
		TransportSelector: newFixedTransportSelector(id1),
		Logger:            log.NewNop(),
		// NoConnectedTransportTimeout is unset (0) = no giving up.
	})
	if err != nil {
		return err
	}
	defer closeMultiAndWait(mt)

	done := make(chan error, 1)
	go func() { done <- mt.Write([]byte("payload")) }()

	// Giving up is disabled, so Write should keep blocking until Close.
	select {
	case err := <-done:
		return fmt.Errorf("iteration %d: Write returned before Close was called: %v", iter, err)
	case <-time.After(50 * time.Millisecond):
		// Keeps blocking as expected.
	}

	go func() { _ = mt.Close() }()

	select {
	case err := <-done:
		if err == nil {
			return fmt.Errorf("iteration %d: Write succeeded after Close (expected an error)", iter)
		}
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("iteration %d: Write did not return within 5s after Close", iter)
	}
}

// TestMultiTransport_GiveUpRacesWithExplicitClose verifies the race between giveUp and an explicit Close.
// It sets an extremely short threshold to provoke giveUp (teardownOnce.Do(...) via updateOverallStatus)
// while calling an explicit Close at almost the same time. It confirms that both return in finite time, that -race is clean and that goleak
// passes, repeating stressIterationsSlow times with shifted timing.
// rand is not used; a fixed list of wait times is cycled so that it is reproducible.
//
// Measured on 2026-07-31 in review: wait was initially time.Duration(i%5) * time.Millisecond (0 to 4ms),
// but it was always below the threshold NoConnectedTransportTimeout=5ms, so giveUp
// never fired, which was a false green (0 firings in 3 iterations normally and 20 in the stress build).
// giveUpRaceWaits explicitly includes values that cross the threshold (8ms/10ms), and the test checks that
// there is at least one iteration overall in which giveUp moved the transport to Disconnected
// before Close (FAIL if there are 0). To hit this reliably even in the normal build with few iterations, a fixed list is cycled
// instead of simply taking iter modulo.
//
// Each iteration includes a fixed wait (wait + 50ms cleanup), so using stressIterations (200 in the
// stress build) as is would inflate the run time. It is real-time dependent, so stressIterationsSlow is used
// (see stress_params_*.go).
func TestMultiTransport_GiveUpRacesWithExplicitClose(t *testing.T) {
	defer goleak.VerifyNone(t)

	var giveUpObservedCount int
	for i := 0; i < stressIterationsSlow; i++ {
		observed, err := giveUpCloseRaceRound(t, i)
		if err != nil {
			t.Fatalf("iteration %d failed: %v", i, err)
		}
		if observed {
			giveUpObservedCount++
		}
	}

	if giveUpObservedCount == 0 {
		t.Fatal("giveUp (teardown due to exceeding the threshold) was never observed. " +
			"Possibly giveUpRaceWaits does not cross the threshold, or giveUp itself is broken")
	}
	t.Logf("giveUp observed in %d/%d iterations", giveUpObservedCount, stressIterationsSlow)
}

// giveUpRaceWaits is the fixed list of wait times that giveUpCloseRaceRound uses in each iteration.
// For NoConnectedTransportTimeout=5ms, it reliably includes both values below the threshold (0ms/3ms) and above it (8ms/5ms/10ms),
// so that the case where giveUp fires is hit even with few iterations.
var giveUpRaceWaits = []time.Duration{
	0,
	3 * time.Millisecond,
	8 * time.Millisecond,
	5 * time.Millisecond,
	10 * time.Millisecond,
}

// giveUpCloseRaceRound runs one iteration of the giveUp/Close race.
// The return value observed indicates whether OverallStatus was already
// Disconnected just before the explicit Close was called (= whether giveUp fired first). Note that Close itself
// also eventually makes all subs Disconnected, so OverallStatus after the Close call
// cannot tell whether giveUp fired.
func giveUpCloseRaceRound(t *testing.T, iter int) (observed bool, err error) {
	t.Helper()

	rt1 := newAlwaysFailingConnectingTransport(t, "sub1")

	id1 := transport.SubConnectionID("transport1")

	mt, err := NewTransport(TransportConfig{
		TransportMap:                TransportMap{id1: rt1},
		TransportSelector:           newFixedTransportSelector(id1),
		Logger:                      log.NewNop(),
		NoConnectedTransportTimeout: 5 * time.Millisecond, // extremely short, to provoke giveUp
		StatusCheckInterval:         2 * time.Millisecond,
	})
	if err != nil {
		return false, err
	}

	// Insert a different wait time in each iteration so as to straddle the timing at which giveUp fires,
	// then call an explicit Close (rand is not used, to keep it reproducible).
	wait := giveUpRaceWaits[iter%len(giveUpRaceWaits)]
	time.Sleep(wait)

	giveUpFiredBeforeClose := mt.OverallStatus() == MultiOverallStatusDisconnected

	closeDone := make(chan error, 1)
	go func() { closeDone <- mt.Close() }()

	select {
	case <-closeDone:
	case <-time.After(5 * time.Second):
		return false, fmt.Errorf("iteration %d: Close did not return within 5s (wait=%v)", iter, wait)
	}

	// Wait for the cleanup to finish, including the giveUp path (started in a separate goroutine).
	time.Sleep(50 * time.Millisecond)
	return giveUpFiredBeforeClose, nil
}

// TestMultiTransport_RepeatedRecoveryJustBeforeThreshold repeats stressIterationsSlow times: bring one transport back to Connected just before the threshold,
// then drop it again; it verifies that the transport does not give up (the measurement of noConnectedTracker is
// reset each time). It runs in real time rather than with fakeClock, aiming to
// catch missed level-trigger detections.
//
// Each iteration includes a fixed time.Sleep (70% of the threshold + the waitForConnected wait), so
// using stressIterations (200 in the stress build) as is would simply inflate the run time.
// It is real-time dependent, so stressIterationsSlow is used (see stress_params_*.go).
func TestMultiTransport_RepeatedRecoveryJustBeforeThreshold(t *testing.T) {
	defer goleak.VerifyNone(t)

	const threshold = 100 * time.Millisecond
	rt1, drop1 := newReopenableReconnectTransport(t, "sub1")
	waitForConnected(t, rt1)

	id1 := transport.SubConnectionID("transport1")
	mt, err := NewTransport(TransportConfig{
		TransportMap:                TransportMap{id1: rt1},
		TransportSelector:           newFixedTransportSelector(id1),
		Logger:                      log.NewNop(),
		NoConnectedTransportTimeout: threshold,
		StatusCheckInterval:         10 * time.Millisecond,
	})
	require.NoError(t, err)
	defer closeMultiAndWait(mt)

	for i := 0; i < stressIterationsSlow; i++ {
		drop1() // Connected -> Reconnecting (the measurement of noConnectedTracker starts)

		// Wait for only 70% of the threshold and then bring it back (aiming to catch missed level-trigger detections).
		time.Sleep(threshold * 7 / 10)
		if mt.OverallStatus() == MultiOverallStatusDisconnected {
			t.Fatalf("iteration %d: overall status became Disconnected before the threshold", i)
		}

		waitForConnected(t, rt1) // wait until it is back to Connected with a new mock
		if mt.OverallStatus() == MultiOverallStatusDisconnected {
			t.Fatalf("iteration %d: overall status is Disconnected right after recovery", i)
		}
	}
}

// TestMultiTransport_BlockedWriteDoesNotFallBackToOtherSub is a test that records the "current behavior":
// while the underlying Write of the selected sub-conn is blocked, multi.Transport.Write keeps blocking
// without falling back to a healthy other sub-conn.
// Whether this is the desired behavior is left undecided; the fact is pinned first.
//
// writeOnce (transport.go:539-590) cannot proceed to the next decision until the underlying Write of the selected sub
// returns, so in an implementation where the underlying Write itself blocks, fallback to
// another sub-conn does not work.
//
// The asserts below confirm that the "undesirable behavior" stays as it is,
// and are written to FAIL if production is improved so that it can fall back.
// If it fails, the test may not be broken; production may have been improved,
// so check writeOnce and then update this test.
func TestMultiTransport_BlockedWriteDoesNotFallBackToOtherSub(t *testing.T) {
	defer goleak.VerifyNone(t)

	blockingMock := newBlockingWriteMockTransport("sub1")
	rt1 := newReconnectTransportWithMock(t, blockingMock, "sub1")
	waitForConnected(t, rt1)

	mock2 := newMockTransport("mock2")
	rt2 := newTestReconnectTransport(t, mock2, "sub2")
	waitForConnected(t, rt2)

	id1 := transport.SubConnectionID("transport1")
	id2 := transport.SubConnectionID("transport2")
	selector := newFixedTransportSelector(id1) // always select sub1 (the one that blocks)

	mt, err := NewTransport(TransportConfig{
		TransportMap:      TransportMap{id1: rt1, id2: rt2},
		TransportSelector: selector,
		Logger:            log.NewNop(),
	})
	require.NoError(t, err)
	defer closeMultiAndWait(mt)

	done := make(chan error, 1)
	go func() { done <- mt.Write([]byte("payload")) }()

	select {
	case err := <-done:
		t.Fatalf("Write returned (err=%v) despite sub1's underlying Write being blocked — fallback may have occurred", err)
	case <-time.After(300 * time.Millisecond):
		t.Log("Confirmed: Write blocked on sub1 without falling back to sub2 (current behavior)")
	}

	// It should not have been written to mock2.
	select {
	case got := <-mock2.writeCh:
		t.Fatalf("payload unexpectedly reached sub2 despite selector choosing sub1: %v", got)
	default:
	}

	// Cleanup: release the blocked Write.
	blockingMock.unblock()
	select {
	case err := <-done:
		t.Logf("Write returned after unblocking: err=%v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Write did not return even after unblocking the underlying Write")
	}
}
