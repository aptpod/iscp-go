package multi_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
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

// fixedTransportSelector is a selector that always returns a fixed SubConnectionID.
// Unlike mockTransportSelector, it does not implement MultiTransportSetter, so
// it can bypass the status-aware fallback by SelectAvailableTransport.
// This makes it possible to intentionally create a case where the sub-conn chosen by the selector is itself Connecting/Reconnecting,
// and to verify the fallback path of multi.Transport.Write itself.
type fixedTransportSelector struct {
	selected transport.SubConnectionID
}

// capturingLogger is a test logger that records only the output of Warnf.
// Other methods are delegated to the embedded Nop logger.
type capturingLogger struct {
	log.Logger
	mu    sync.Mutex
	warns []string
}

func newCapturingLogger() *capturingLogger {
	return &capturingLogger{Logger: log.NewNop()}
}

func (l *capturingLogger) Warnf(_ context.Context, format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warns = append(l.warns, fmt.Sprintf(format, args...))
}

func (l *capturingLogger) joinedWarns() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.warns, "\n")
}

func newFixedTransportSelector(selected transport.SubConnectionID) *fixedTransportSelector {
	return &fixedTransportSelector{selected: selected}
}

func (s *fixedTransportSelector) Get(_ context.Context, _ int64) transport.SubConnectionID {
	return s.selected
}

// TestReconnectTransport_Write_DoesNotBlockOnUnlimitedRetry is a regression test that
// guarantees reconnect.Transport.Write() returns an error promptly even when
// MaxReconnectAttempts=-1 and the dialer blocks forever.
func TestReconnectTransport_Write_DoesNotBlockOnUnlimitedRetry(t *testing.T) {
	blockDial := make(chan struct{})
	var dialCount atomic.Int32
	mock := newMockTransport("mock1")

	rt, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			if dialCount.Add(1) == 1 {
				return mock, nil
			}
			<-blockDial
			return nil, errors.New("test: dialer canceled")
		}),
		DialConfig: transport.DialConfig{
			SubConnectionID:   "sub1",
			SuperConnectionID: transport.SuperConnectionID(testSuperConnectionID),
			TransportType:     transport.NegotiationNameWebSocket,
		},
		MaxReconnectAttempts: -1,
		ReconnectInterval:    10 * time.Millisecond,
		HeartbeatInterval:    time.Hour,
		HeartbeatTimeout:     time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		close(blockDial)
		_ = rt.Close()
	})

	require.Eventually(t,
		func() bool { return rt.Status() == reconnect.StatusConnected },
		5*time.Second, 10*time.Millisecond,
	)

	mock.Close()
	require.Eventually(t,
		func() bool { return rt.Status() == reconnect.StatusReconnecting },
		5*time.Second, 10*time.Millisecond,
	)

	done := make(chan error, 1)
	go func() { done <- rt.Write([]byte("test data")) }()

	select {
	case err := <-done:
		require.Error(t, err, "Write should return an error during unlimited-retry reconnect")
	case <-time.After(2 * time.Second):
		t.Fatal("Write blocked >2s during unlimited-retry reconnect")
	}
}

// TestMultiTransport_Write_FallbackDuringUnlimitedRetry is a regression test that
// guarantees that, even when one of several sub-conns is retrying with MaxReconnectAttempts=-1 with a blocked destination,
// multi.Transport.Write() falls back to another healthy sub-conn and does not
// block forever.
func TestMultiTransport_Write_FallbackDuringUnlimitedRetry(t *testing.T) {
	blockDial1 := make(chan struct{})
	var dialCount1 atomic.Int32
	mock1 := newMockTransport("mock1")

	rt1, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			if dialCount1.Add(1) == 1 {
				return mock1, nil
			}
			<-blockDial1
			return nil, errors.New("test: dialer canceled")
		}),
		DialConfig: transport.DialConfig{
			SubConnectionID:   "sub1",
			SuperConnectionID: transport.SuperConnectionID(testSuperConnectionID),
			TransportType:     transport.NegotiationNameWebSocket,
		},
		MaxReconnectAttempts: -1,
		ReconnectInterval:    10 * time.Millisecond,
		HeartbeatInterval:    time.Hour,
		HeartbeatTimeout:     time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)

	mock2 := newMockTransport("mock2")
	rt2 := newTestReconnectTransport(t, mock2, "sub2")

	waitForConnected(t, rt1)
	waitForConnected(t, rt2)

	id1 := transport.SubConnectionID("transport1")
	id2 := transport.SubConnectionID("transport2")
	selector := newMockTransportSelector(id1)

	mt, err := NewTransport(TransportConfig{
		TransportMap: TransportMap{
			id1: rt1,
			id2: rt2,
		},
		TransportSelector:   selector,
		Logger:              log.NewNop(),
		StatusCheckInterval: 100 * time.Millisecond,
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		close(blockDial1)
		_ = mt.Close()
		time.Sleep(200 * time.Millisecond)
	})

	// If multi.Write is called with sub1 fixed in the Reconnecting state,
	// SelectAvailableTransport returns sub2, so it is written to mock2.
	mock1.Close()
	require.Eventually(t,
		func() bool { return rt1.Status() == reconnect.StatusReconnecting },
		5*time.Second, 10*time.Millisecond,
	)

	done := make(chan error, 1)
	go func() { done <- mt.Write([]byte("multi fallback data")) }()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("multi.Transport.Write blocked >2s despite healthy sub2 available")
	}

	select {
	case received := <-mock2.writeCh:
		require.NotEmpty(t, received)
	case <-time.After(time.Second):
		t.Fatal("write did not reach sub2 within 1s")
	}
}

// TestReconnectTransport_Write_FailsAfterFiniteRetriesExhausted is a regression test that
// guarantees that after all reconnects fail with a finite MaxReconnectAttempts, Write()
// returns an error promptly instead of polling forever.
// (This is a countermeasure for the bug where waitForWritable hangs when Status stays fixed at Reconnecting.)
func TestReconnectTransport_Write_FailsAfterFiniteRetriesExhausted(t *testing.T) {
	var dialCount atomic.Int32
	mock := newMockTransport("mock1")

	rt, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			if dialCount.Add(1) == 1 {
				return mock, nil
			}
			return nil, errors.New("test: dialer always fails on reconnect")
		}),
		DialConfig: transport.DialConfig{
			SubConnectionID:   "sub1",
			SuperConnectionID: transport.SuperConnectionID(testSuperConnectionID),
			TransportType:     transport.NegotiationNameWebSocket,
		},
		MaxReconnectAttempts: 2,
		ReconnectInterval:    10 * time.Millisecond,
		HeartbeatInterval:    time.Hour,
		HeartbeatTimeout:     time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })

	require.Eventually(t,
		func() bool { return rt.Status() == reconnect.StatusConnected },
		5*time.Second, 10*time.Millisecond,
	)

	mock.Close()
	require.Eventually(t,
		func() bool { return rt.Status() == reconnect.StatusDisconnected },
		5*time.Second, 10*time.Millisecond,
		"status should transition to Disconnected after retries are exhausted",
	)

	done := make(chan error, 1)
	go func() { done <- rt.Write([]byte("data")) }()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Write blocked >2s after reconnect retries were exhausted")
	}
}

// TestMultiTransport_Write_DoesNotFallbackOnPartialSendError is a regression test that
// guarantees that when the underlying Write of a sub-conn returns an error after a partial send,
// multi.Transport does not resend the same payload to another sub-conn (to prevent
// duplication/corruption).
//
// The Dialer of sub1 returns mock1 only for the first connection and always returns an error afterwards
// (the same pattern as TestReconnectTransport_Write_FailsAfterFiniteRetriesExhausted).
// This prevents the situation where, even if readLoop detects the disconnect after mock1.Close() and tries to reconnect,
// it is wrongly judged a "successful reconnect" with the same broken mock1 and returns to StatusConnected,
// so the timing window for issuing Write right after mock1.Close() (while still in StatusConnected)
// becomes stable (in the first version the Dialer always returned the same mock1, so the reconnect
// success/failure cycle repeated, and depending on load sub1 was sometimes judged to be
// something other than Connected at Write time and fell back to sub2).
func TestMultiTransport_Write_DoesNotFallbackOnPartialSendError(t *testing.T) {
	mock1 := newMockTransport("mock1")
	var dialCount1 atomic.Int32
	rt1, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			if dialCount1.Add(1) == 1 {
				return mock1, nil
			}
			return nil, errors.New("test: reconnect always fails after initial connect")
		}),
		DialConfig: transport.DialConfig{
			SubConnectionID:   "sub1",
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
	t.Cleanup(func() { _ = rt1.Close() })

	mock2 := newMockTransport("mock2")
	rt2 := newTestReconnectTransport(t, mock2, "sub2")
	waitForConnected(t, rt1)
	waitForConnected(t, rt2)

	id1 := transport.SubConnectionID("transport1")
	id2 := transport.SubConnectionID("transport2")
	selector := newMockTransportSelector(id1)

	mt, err := NewTransport(TransportConfig{
		TransportMap: TransportMap{
			id1: rt1,
			id2: rt2,
		},
		TransportSelector:   selector,
		Logger:              log.NewNop(),
		StatusCheckInterval: 100 * time.Millisecond,
	})
	require.NoError(t, err)
	t.Cleanup(func() { closeAndWait(t, mt) })

	// Closing mock1 makes the underlying tr.Write inside the reconnect.Transport of sub1
	// return a "transport closed" error (by design, the Write of the mock returns an error when isClosed).
	// This is not ErrNotConnected (treated as equivalent to a partial send).
	mock1.Close()

	// If Write is issued right after (while Status is still StatusConnected),
	// waitForWritable sees Connected and calls tr.Write, which returns an error as a result.
	// This error is not wrapped with ErrNotConnected, so multi does not fall back to sub2.
	err = mt.Write([]byte("payload"))
	require.Error(t, err, "expected error from sub1 mid-write failure")
	require.False(t, errors.Is(err, reconnect.ErrNotConnected),
		"mid-write error should not be classified as ErrNotConnected")

	// Verify that the payload did not flow to mock2
	select {
	case got := <-mock2.writeCh:
		t.Fatalf("payload unexpectedly resent to sub2: %v", got)
	case <-time.After(100 * time.Millisecond):
		// expected: nothing is written to sub2
	}
}

// TestMultiTransport_Write_FallbacksOnAlreadyClosedError is the positive case
// paired with TestMultiTransport_Write_DoesNotFallbackOnPartialSendError.
// It verifies that when the underlying Write of sub1 fails with transport.ErrAlreadyClosed
// (exactly what the TOCTOU countermeasure of writeRaw converts to ErrNotConnected),
// multi.Transport falls back to sub2 and the payload
// is delivered.
//
// In reconnect.Transport.writeRaw, when the error of the underlying tr.Write is
// errors.Is(err, errors.ErrConnectionClosed)(== transport.ErrAlreadyClosed)
// it is wrapped with reconnect.ErrNotConnected (TOCTOU fix). Even if this conversion is
// removed, the existing websocket-side tests do not go through the conversion layer of reconnect,
// so it cannot be detected (false green). This test goes through reconnect.Transport and verifies up to the fallback of
// multi.Transport, which guarantees that the presence of this conversion
// actually affects the behavior.
func TestMultiTransport_Write_FallbacksOnAlreadyClosedError(t *testing.T) {
	mock1 := newMockTransport("mock1")
	mock1.writeErrWhenClosed = transport.ErrAlreadyClosed
	var dialCount1 atomic.Int32
	rt1, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			if dialCount1.Add(1) == 1 {
				return mock1, nil
			}
			return nil, errors.New("test: reconnect always fails after initial connect")
		}),
		DialConfig: transport.DialConfig{
			SubConnectionID:   "sub1",
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
	t.Cleanup(func() { _ = rt1.Close() })

	mock2 := newMockTransport("mock2")
	rt2 := newTestReconnectTransport(t, mock2, "sub2")
	waitForConnected(t, rt1)
	waitForConnected(t, rt2)

	id1 := transport.SubConnectionID("transport1")
	id2 := transport.SubConnectionID("transport2")
	selector := newMockTransportSelector(id1)

	mt, err := NewTransport(TransportConfig{
		TransportMap: TransportMap{
			id1: rt1,
			id2: rt2,
		},
		TransportSelector:   selector,
		Logger:              log.NewNop(),
		StatusCheckInterval: 100 * time.Millisecond,
	})
	require.NoError(t, err)
	t.Cleanup(func() { closeAndWait(t, mt) })

	// Closing mock1 makes the underlying tr.Write inside the reconnect.Transport of sub1
	// return transport.ErrAlreadyClosed. Because of the TOCTOU countermeasure of writeRaw,
	// this becomes ErrNotConnected and is subject to fallback.
	mock1.Close()

	err = mt.Write([]byte("payload"))
	require.NoError(t, err, "should fall back to sub2 when sub1's underlying Write fails with ErrAlreadyClosed")

	select {
	case got := <-mock2.writeCh:
		// With useV4Protocol, a 1-byte type tag is prepended to the payload, so
		// match by suffix instead of exact match (as in TestMultiTransport_Write_FallbackDuringUnlimitedRetry,
		// the focus is on "it arrived" rather than the content itself).
		require.True(t, bytes.HasSuffix(got, []byte("payload")), "unexpected payload: %v", got)
	case <-time.After(time.Second):
		t.Fatal("payload did not reach sub2 within 1s")
	}
}

// TestMultiTransport_Write_LogsWarningOnFallback verifies that when a write to the selected sub-conn
// fails with writeTimeout (context.DeadlineExceeded) and the fallback
// succeeds, a warning log is left.
//
// writeOnce does not return an error upward when the fallback succeeds, so without the log
// it would not be observable, either from the application or from
// operations, that "one sub-conn stalled until writeTimeout".
func TestMultiTransport_Write_LogsWarningOnFallback(t *testing.T) {
	mock1 := newMockTransport("mock1")
	rt1 := newTestReconnectTransport(t, mock1, "sub1")
	mock2 := newMockTransport("mock2")
	rt2 := newTestReconnectTransport(t, mock2, "sub2")

	waitForConnected(t, rt1)
	waitForConnected(t, rt2)

	// Read stays blocked and only Write fails, so the Status of rt1 is kept
	// Connected and the selector keeps choosing rt1. This lets the fallback path
	// in writeOnce be exercised independently of timing.
	mock1.SetAlwaysFailWrite(context.DeadlineExceeded)

	id1 := transport.SubConnectionID("transport1")
	id2 := transport.SubConnectionID("transport2")
	selector := newMockTransportSelector(id1)

	logger := newCapturingLogger()
	mt, err := NewTransport(TransportConfig{
		TransportMap: TransportMap{
			id1: rt1,
			id2: rt2,
		},
		TransportSelector:   selector,
		Logger:              logger,
		StatusCheckInterval: 100 * time.Millisecond,
	})
	require.NoError(t, err)
	t.Cleanup(func() { closeMultiAndWait(mt) })

	require.NoError(t, mt.Write([]byte("payload")))

	select {
	case got := <-mock2.writeCh:
		require.True(t, bytes.HasSuffix(got, []byte("payload")), "unexpected payload: %v", got)
	case <-time.After(time.Second):
		t.Fatal("payload did not reach sub2 within 1s")
	}

	warns := logger.joinedWarns()
	require.Contains(t, warns, "Write fell back from transport",
		"a successful fallback does not return an error upward, so the log is the only observation point")
	require.Contains(t, warns, context.DeadlineExceeded.Error(),
		"it must be possible to tell from the log that it is caused by writeTimeout")
}

// TestMultiTransport_Write_RetriesWhenAllSubsFailWithAlreadyClosedError is an additional case.
// The TOCTOU countermeasure of writeRaw (when the underlying Write returns
// errors.ErrConnectionClosed, it is converted to reconnect.ErrNotConnected)
// has no visible effect when only one sub fails and can fall back to another sub,
// because isFallbackableWriteError directly checks transport.ErrAlreadyClosed
// (see TestMultiTransport_Write_FallbacksOnAlreadyClosedError).
// The effect becomes observable when "all subs fail in the underlying Write", and
// multi.Transport.Write continues its internal retry only on errAllNotConnected (= all subs
// failed with reconnect.ErrNotConnected)
// (transport.go:518-533, 601-605). If the underlying error is not converted to ErrNotConnected,
// allNotConnected becomes false and Write returns an error immediately.
//
// If both sub-conns are dropped with mock1.Close()/mock2.Close(), the timing window in which the Status of reconnect.Transport
// transitions from Connected to Reconnecting/Disconnected must be hit
// for both at the same time, which is unstable (it was flaky with the dialCount pattern too).
// So the Read of the mock stays blocked and only Write always fails,
// creating a situation where only the underlying Write fails while Status stays Connected,
// independently of timing.
func TestMultiTransport_Write_RetriesWhenAllSubsFailWithAlreadyClosedError(t *testing.T) {
	mock1 := newMockTransport("mock1")
	rt1 := newTestReconnectTransport(t, mock1, "sub1")
	mock2 := newMockTransport("mock2")
	rt2 := newTestReconnectTransport(t, mock2, "sub2")

	waitForConnected(t, rt1)
	waitForConnected(t, rt2)

	mock1.SetAlwaysFailWrite(transport.ErrAlreadyClosed)
	mock2.SetAlwaysFailWrite(transport.ErrAlreadyClosed)

	id1 := transport.SubConnectionID("transport1")
	id2 := transport.SubConnectionID("transport2")
	selector := newMockTransportSelector(id1)

	mt, err := NewTransport(TransportConfig{
		TransportMap: TransportMap{
			id1: rt1,
			id2: rt2,
		},
		TransportSelector:   selector,
		Logger:              log.NewNop(),
		StatusCheckInterval: 100 * time.Millisecond,
	})
	require.NoError(t, err)
	t.Cleanup(func() { closeMultiAndWait(mt) })

	done := make(chan error, 1)
	go func() { done <- mt.Write([]byte("payload")) }()

	// If writeRaw converts to ErrNotConnected, allNotConnected=true
	// and Write keeps retrying internally and keeps blocking until
	// some sub recovers. If it is not converted, it returns an error immediately.
	select {
	case err := <-done:
		t.Fatalf("Write should keep retrying (stay blocked) when all subs fail with ErrAlreadyClosed, got: %v", err)
	case <-time.After(300 * time.Millisecond):
		// Keeps blocking as expected.
	}
}

// newAlwaysFailingConnectingTransport creates a reconnect.Transport that always fails from the first connection and
// stays fixed at StatusConnecting.
func newAlwaysFailingConnectingTransport(t *testing.T, subConnID string) *reconnect.Transport {
	t.Helper()
	rt, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			return nil, errors.New("test: initial dial always fails")
		}),
		DialConfig: transport.DialConfig{
			SubConnectionID:   transport.SubConnectionID(subConnID),
			SuperConnectionID: transport.SuperConnectionID(testSuperConnectionID),
			TransportType:     transport.NegotiationNameWebSocket,
		},
		MaxReconnectAttempts: -1, // subs always retry without limit
		ReconnectInterval:    10 * time.Millisecond,
		HeartbeatInterval:    time.Hour,
		HeartbeatTimeout:     time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)
	return rt
}

// closeMultiAndWait closes the multi.Transport and waits for the goroutines to finish.
//
// **Use it with defer, not t.Cleanup.** t.Cleanup runs after all defers of the test function
// have run, so closing in Cleanup is too late for the check of
// goleak.VerifyNone registered with defer (statusMonitorLoop / readLoop /
// initialConnect and others are still alive when it is checked, and it always fails as a leak).
// Because defers are LIFO, this helper, registered after goleak.VerifyNone, runs first.
//
// giveUp due to exceeding the threshold may have already closed it, so the error is ignored
// (the existing closeAndWait does require.NoError, so it cannot be used for this purpose).
func closeMultiAndWait(mt *Transport) {
	_ = mt.Close()
	time.Sleep(200 * time.Millisecond)
}

// TestMultiTransport_WriteReleasedWhenAllSubsStayUnconnectedPastThreshold verifies that
// Write is released once all subs stay unconnected beyond the threshold.
//
//   - Before the threshold: all subs are Connecting, so waitForWritable blocks and
//     multi.Write does not return an error immediately (a normal transient state right after Open)
//   - After the threshold: the parent closes all subs, so the blocked Write
//     returns an error and OverallStatus becomes Disconnected
func TestMultiTransport_WriteReleasedWhenAllSubsStayUnconnectedPastThreshold(t *testing.T) {
	defer goleak.VerifyNone(t)

	rt1 := newAlwaysFailingConnectingTransport(t, "sub1")
	rt2 := newAlwaysFailingConnectingTransport(t, "sub2")

	id1 := transport.SubConnectionID("transport1")
	id2 := transport.SubConnectionID("transport2")

	// mockTransportSelector falls back status-aware via SelectAvailableTransport,
	// but here all subs are non-Connected, so the result does not change.
	// A fixed selector is used to reliably reach the Connecting branch of waitForWritable.
	selector := newFixedTransportSelector(id1)

	const timeout = 300 * time.Millisecond
	mt, err := NewTransport(TransportConfig{
		TransportMap:                TransportMap{id1: rt1, id2: rt2},
		TransportSelector:           selector,
		Logger:                      log.NewNop(),
		NoConnectedTransportTimeout: timeout,
	})
	require.NoError(t, err)
	defer closeMultiAndWait(mt)

	done := make(chan error, 1)
	go func() { done <- mt.Write([]byte("payload")) }()

	// Before the threshold: it does not return an error immediately.
	select {
	case err := <-done:
		t.Fatalf("Write returned an error before the threshold: %v", err)
	case <-time.After(timeout / 2):
		// Keeps blocking as expected.
	}

	// After the threshold: Write returns an error.
	select {
	case err := <-done:
		require.Error(t, err, "Write after exceeding the threshold should return an error")
	case <-time.After(5 * time.Second):
		t.Fatal("Write was not released even after exceeding the threshold")
	}

	require.Equal(t, MultiOverallStatusDisconnected, mt.OverallStatus())
}

// TestMultiTransport_GivesUpWhenAllSubsStayReconnectingPastThreshold verifies that the transport gives up once all subs stay Reconnecting beyond the threshold.
// Before the threshold, Write returns an error immediately (the Reconnecting branch of waitForWritable),
// which is by design asymmetric with the Connecting version.
func TestMultiTransport_GivesUpWhenAllSubsStayReconnectingPastThreshold(t *testing.T) {
	defer goleak.VerifyNone(t)

	mock1 := newMockTransport("mock1")
	rt1 := newFailingReconnectTransport(t, mock1, "sub1")
	mock2 := newMockTransport("mock2")
	rt2 := newFailingReconnectTransport(t, mock2, "sub2")

	id1 := transport.SubConnectionID("transport1")
	id2 := transport.SubConnectionID("transport2")

	waitForConnected(t, rt1)
	waitForConnected(t, rt2)

	mt, err := NewTransport(TransportConfig{
		TransportMap:                TransportMap{id1: rt1, id2: rt2},
		TransportSelector:           newMockTransportSelector(id1),
		Logger:                      log.NewNop(),
		NoConnectedTransportTimeout: 300 * time.Millisecond,
	})
	require.NoError(t, err)
	defer closeMultiAndWait(mt)

	// When both underlying transports are dropped, the dialer of newFailingReconnectTransport always fails afterwards,
	// and since MaxReconnectAttempts=-1 it stays fixed at Reconnecting.
	mock1.Close()
	mock2.Close()

	require.Eventually(t,
		func() bool {
			return rt1.Status() == reconnect.StatusReconnecting &&
				rt2.Status() == reconnect.StatusReconnecting
		},
		5*time.Second, 10*time.Millisecond,
		"both subs should be Reconnecting")

	// Revised on 2026-07-31: even when all subs are Reconnecting, multi.Write
	// keeps blocking until the threshold. Symmetric with the Connecting version.
	done := make(chan error, 1)
	go func() { done <- mt.Write([]byte("payload")) }()

	select {
	case err := <-done:
		t.Fatalf("Write returned an error before the threshold: %v", err)
	case <-time.After(150 * time.Millisecond): // half of the 300ms threshold
		// Keeps blocking as expected.
	}

	// After the threshold, it returns an error.
	select {
	case err := <-done:
		require.Error(t, err, "Write after exceeding the threshold should return an error")
	case <-time.After(5 * time.Second):
		t.Fatal("Write was not released even after exceeding the threshold")
	}

	require.Eventually(t,
		func() bool { return mt.OverallStatus() == MultiOverallStatusDisconnected },
		5*time.Second, 20*time.Millisecond,
		"if all subs stay Reconnecting beyond the threshold, it should become Disconnected")
}

// TestMultiTransport_DoesNotGiveUpWhenRecoveredBeforeThreshold verifies that the transport does not give up when a sub recovers before the threshold.
func TestMultiTransport_DoesNotGiveUpWhenRecoveredBeforeThreshold(t *testing.T) {
	defer goleak.VerifyNone(t)

	mock1 := newMockTransport("mock1")
	rt1 := newTestReconnectTransport(t, mock1, "sub1")
	rt2 := newAlwaysFailingConnectingTransport(t, "sub2")

	id1 := transport.SubConnectionID("transport1")
	id2 := transport.SubConnectionID("transport2")

	waitForConnected(t, rt1)

	mt, err := NewTransport(TransportConfig{
		TransportMap:                TransportMap{id1: rt1, id2: rt2},
		TransportSelector:           newMockTransportSelector(id1),
		Logger:                      log.NewNop(),
		NoConnectedTransportTimeout: 200 * time.Millisecond,
	})
	require.NoError(t, err)
	defer closeMultiAndWait(mt)

	// sub1 is Connected, so it does not give up no matter how many times the threshold elapses.
	require.Never(t,
		func() bool { return mt.OverallStatus() == MultiOverallStatusDisconnected },
		1*time.Second, 20*time.Millisecond,
		"as long as even one sub is Connected, the measurement keeps being reset")
}

// TestMultiTransport_DoesNotGiveUpWhenThresholdIsZero verifies that the transport does not give up when the threshold is 0.
// Passing MaxReconnectAttempts=-1 (unlimited) through CalcNoConnectedTransportTimeout yields 0,
// which falls into this path.
func TestMultiTransport_DoesNotGiveUpWhenThresholdIsZero(t *testing.T) {
	defer goleak.VerifyNone(t)

	rt1 := newAlwaysFailingConnectingTransport(t, "sub1")
	rt2 := newAlwaysFailingConnectingTransport(t, "sub2")

	id1 := transport.SubConnectionID("transport1")
	id2 := transport.SubConnectionID("transport2")

	mt, err := NewTransport(TransportConfig{
		TransportMap:      TransportMap{id1: rt1, id2: rt2},
		TransportSelector: newMockTransportSelector(id1),
		Logger:            log.NewNop(),
		// NoConnectedTransportTimeout is unset (= 0).
		StatusCheckInterval: 20 * time.Millisecond,
	})
	require.NoError(t, err)
	defer closeMultiAndWait(mt)

	require.Never(t,
		func() bool { return mt.OverallStatus() == MultiOverallStatusDisconnected },
		1*time.Second, 20*time.Millisecond,
		"with threshold 0 (disabled), it does not give up no matter how many hours pass (compatible with current behavior)")
}
