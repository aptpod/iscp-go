package multi_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/aptpod/iscp-go/v2/log"
	"github.com/aptpod/iscp-go/v2/transport"
	. "github.com/aptpod/iscp-go/v2/transport/multi"
	"github.com/aptpod/iscp-go/v2/transport/reconnect"
)

// ---------- Mock Transport ----------

// mockTransport is a mock that implements transport.Transport and transport.Closer.
// It is used as the underlying transport of reconnect.Transport.
type mockTransport struct {
	readCh   chan []byte
	writeCh  chan []byte
	closeCh  chan struct{}
	mu       sync.Mutex
	isClosed bool
	name     transport.Name

	// If writeErrWhenClosed is set, Write returns this error when isClosed
	// (if unset, it returns the default generic "transport closed" error).
	writeErrWhenClosed error

	// If alwaysFailWriteErr is set, Write always returns this error regardless of isClosed.
	// Read stays blocked, so readLoop does not detect a disconnect
	// and the Status of reconnect.Transport stays Connected
	// (used to create a situation where only the underlying Write fails, stably and without
	// a timing window).
	alwaysFailWriteErr error
}

func newMockTransport(name string) *mockTransport {
	return &mockTransport{
		readCh:  make(chan []byte, 100),
		writeCh: make(chan []byte, 100),
		closeCh: make(chan struct{}),
		name:    transport.Name(name),
	}
}

func (m *mockTransport) Read() ([]byte, error) {
	select {
	case data := <-m.readCh:
		return data, nil
	case <-m.closeCh:
		return nil, errors.New("transport closed")
	}
}

func (m *mockTransport) Write(bs []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.alwaysFailWriteErr != nil {
		return m.alwaysFailWriteErr
	}
	if m.isClosed {
		if m.writeErrWhenClosed != nil {
			return m.writeErrWhenClosed
		}
		return errors.New("transport closed")
	}
	m.writeCh <- bs
	return nil
}

// SetAlwaysFailWrite makes every later Write call fail with err.
func (m *mockTransport) SetAlwaysFailWrite(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.alwaysFailWriteErr = err
}

func (m *mockTransport) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.isClosed {
		m.isClosed = true
		close(m.closeCh)
	}
	return nil
}

func (m *mockTransport) CloseWithStatus(_ transport.CloseStatus) error {
	return m.Close()
}

func (m *mockTransport) IsClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.isClosed
}

func (m *mockTransport) Name() transport.Name {
	return m.name
}

func (m *mockTransport) NegotiationParams() transport.NegotiationParams {
	return transport.NegotiationParams{}
}

func (m *mockTransport) AsUnreliable() (transport.UnreliableTransport, bool) {
	return nil, false
}

func (m *mockTransport) RxBytesCounterValue() uint64 { return 0 }
func (m *mockTransport) TxBytesCounterValue() uint64 { return 0 }

// ---------- Mock Transport Selector ----------

// mockTransportSelector is a mock that implements TransportSelector and MultiTransportSetter.
type mockTransportSelector struct {
	mu             sync.Mutex
	selected       transport.SubConnectionID
	multiTransport *Transport
}

func newMockTransportSelector(selected transport.SubConnectionID) *mockTransportSelector {
	return &mockTransportSelector{selected: selected}
}

func (s *mockTransportSelector) Get(_ context.Context, bsSize int64) transport.SubConnectionID {
	s.mu.Lock()
	id := s.selected
	mt := s.multiTransport
	s.mu.Unlock()

	if mt != nil {
		return SelectAvailableTransport(id, mt.Transports())
	}
	return id
}

func (s *mockTransportSelector) SetMultiTransport(mt *Transport) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.multiTransport = mt
}

// ---------- Helpers ----------

const testSuperConnectionID = "test-super-connection"

// newTestReconnectTransport creates a reconnect.Transport for tests.
// It uses a dialer that always returns the same mockTransport.
func newTestReconnectTransport(t *testing.T, mock *mockTransport, subConnID string) *reconnect.Transport {
	t.Helper()
	rt, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			return mock, nil
		}),
		DialConfig: transport.DialConfig{
			SubConnectionID:   transport.SubConnectionID(subConnID),
			SuperConnectionID: transport.SuperConnectionID(testSuperConnectionID),
			TransportType:     transport.NegotiationNameWebSocket, // enable the v4 protocol
		},
		MaxReconnectAttempts: 1,
		ReconnectInterval:    10 * time.Millisecond,
		HeartbeatInterval:    1 * time.Hour,
		HeartbeatTimeout:     1 * time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)
	return rt
}

// newFailingReconnectTransport creates a reconnect.Transport that succeeds only on the first connection and always fails afterwards.
// Reconnect attempts are unlimited, and it stays in the StatusReconnecting state.
func newFailingReconnectTransport(t *testing.T, mock *mockTransport, subConnID string) *reconnect.Transport {
	t.Helper()
	var mu sync.Mutex
	connected := false
	rt, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			mu.Lock()
			defer mu.Unlock()
			if !connected {
				connected = true
				return mock, nil
			}
			return nil, errors.New("connection refused")
		}),
		DialConfig: transport.DialConfig{
			SubConnectionID:   transport.SubConnectionID(subConnID),
			SuperConnectionID: transport.SuperConnectionID(testSuperConnectionID),
			TransportType:     transport.NegotiationNameWebSocket, // enable the v4 protocol
		},
		MaxReconnectAttempts: -1, // unlimited retries keep it in the Reconnecting state
		ReconnectInterval:    10 * time.Millisecond,
		HeartbeatInterval:    1 * time.Hour,
		HeartbeatTimeout:     1 * time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)
	return rt
}

func waitForConnected(t *testing.T, rt *reconnect.Transport) {
	t.Helper()
	require.Eventually(t,
		func() bool { return rt.Status() == reconnect.StatusConnected },
		5*time.Second, 10*time.Millisecond,
		"reconnect transport should become Connected",
	)
}

// iscpMessage creates an iSCP message frame (0x00 + data).
func iscpMessage(data []byte) []byte {
	msg := make([]byte, len(data)+1)
	msg[0] = 0x00 // MessageTypeISCP
	copy(msg[1:], data)
	return msg
}

// closeAndWait closes the multi.Transport and waits for the goroutines to finish.
func closeAndWait(t *testing.T, mt *Transport) {
	t.Helper()
	err := mt.Close()
	require.NoError(t, err)
	time.Sleep(200 * time.Millisecond)
}

// ---------- Tests ----------

func TestNewMultiTransport(t *testing.T) {
	defer goleak.VerifyNone(t)

	mock1 := newMockTransport("mock1")
	mock2 := newMockTransport("mock2")

	rt1 := newTestReconnectTransport(t, mock1, "sub1")
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
	assert.NotNil(t, mt)

	// Verify that Name contains "multiple"
	name := string(mt.Name())
	assert.Contains(t, name, "multiple")

	// Verify that TransportMap contains 2 transports
	transports := mt.Transports()
	assert.Len(t, transports, 2)
	assert.Contains(t, transports, id1)
	assert.Contains(t, transports, id2)

	closeAndWait(t, mt)
}

func TestMultiTransport_ReadWrite(t *testing.T) {
	defer goleak.VerifyNone(t)

	mock1 := newMockTransport("mock1")
	rt1 := newTestReconnectTransport(t, mock1, "sub1")
	waitForConnected(t, rt1)

	id1 := transport.SubConnectionID("transport1")
	selector := newMockTransportSelector(id1)

	mt, err := NewTransport(TransportConfig{
		TransportMap: TransportMap{
			id1: rt1,
		},
		TransportSelector:   selector,
		Logger:              log.NewNop(),
		StatusCheckInterval: 100 * time.Millisecond,
	})
	require.NoError(t, err)

	t.Run("write operation", func(t *testing.T) {
		testData := []byte("test data")
		err := mt.Write(testData)
		require.NoError(t, err)

		select {
		case received := <-mock1.writeCh:
			// reconnect.Transport adds the MessageTypeISCP (0x00) prefix
			assert.Equal(t, iscpMessage(testData), received)
		case <-time.After(time.Second):
			t.Fatal("write timeout")
		}
	})

	t.Run("read operation", func(t *testing.T) {
		testData := []byte("test response")
		// Send data in the format that readLoop of reconnect.Transport expects
		mock1.readCh <- iscpMessage(testData)

		received, err := mt.Read()
		require.NoError(t, err)
		assert.Equal(t, testData, received)
	})

	closeAndWait(t, mt)
}

func TestMultiTransport_Write_Fallback(t *testing.T) {
	defer goleak.VerifyNone(t)

	mock1 := newMockTransport("mock1")
	mock2 := newMockTransport("mock2")

	// transport1: connects only the first time and fails afterwards (stays Reconnecting)
	rt1 := newFailingReconnectTransport(t, mock1, "sub1")
	// transport2: always connects successfully
	rt2 := newTestReconnectTransport(t, mock2, "sub2")
	waitForConnected(t, rt1)
	waitForConnected(t, rt2)

	id1 := transport.SubConnectionID("transport1")
	id2 := transport.SubConnectionID("transport2")

	// A selector that prefers transport1
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

	// Close the underlying mock of transport1 (simulate a disconnect)
	mock1.Close()

	// Wait until transport1 becomes Reconnecting
	require.Eventually(t,
		func() bool { return rt1.Status() == reconnect.StatusReconnecting },
		5*time.Second, 10*time.Millisecond,
		"transport1 should become Reconnecting",
	)

	// Write - the selector selects transport1, but SelectAvailableTransport
	// falls back to transport2
	testData := []byte("fallback test data")
	err = mt.Write(testData)
	require.NoError(t, err)

	// Verify that data was written to transport2
	select {
	case received := <-mock2.writeCh:
		assert.Equal(t, iscpMessage(testData), received)
	case <-time.After(time.Second):
		t.Fatal("write timeout on fallback transport")
	}

	// Verify that no data was written to transport1
	select {
	case <-mock1.writeCh:
		t.Fatal("data should not be written to disconnected transport")
	default:
		// expected
	}

	closeAndWait(t, mt)
}

func TestMultiTransport_Write_AllDisconnected(t *testing.T) {
	defer goleak.VerifyNone(t)

	// Both dialers always fail (the first connection fails too)
	rt1, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			return nil, errors.New("connection refused")
		}),
		DialConfig: transport.DialConfig{
			SubConnectionID:   "sub1",
			SuperConnectionID: transport.SuperConnectionID(testSuperConnectionID),
		},
		MaxReconnectAttempts: 1,
		ReconnectInterval:    10 * time.Millisecond,
		HeartbeatInterval:    1 * time.Hour,
		HeartbeatTimeout:     1 * time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)

	rt2, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			return nil, errors.New("connection refused")
		}),
		DialConfig: transport.DialConfig{
			SubConnectionID:   "sub2",
			SuperConnectionID: transport.SuperConnectionID(testSuperConnectionID),
		},
		MaxReconnectAttempts: 1,
		ReconnectInterval:    10 * time.Millisecond,
		HeartbeatInterval:    1 * time.Hour,
		HeartbeatTimeout:     1 * time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)

	// Wait until both become Disconnected
	require.Eventually(t,
		func() bool { return rt1.Status() == reconnect.StatusDisconnected },
		5*time.Second, 10*time.Millisecond,
		"transport1 should become Disconnected",
	)
	require.Eventually(t,
		func() bool { return rt2.Status() == reconnect.StatusDisconnected },
		5*time.Second, 10*time.Millisecond,
		"transport2 should become Disconnected",
	)

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

	// Wait until OverallStatus becomes Disconnected
	require.Eventually(t,
		func() bool { return mt.OverallStatus() == MultiOverallStatusDisconnected },
		5*time.Second, 10*time.Millisecond,
		"overall status should become Disconnected",
	)

	// Write should fail
	err = mt.Write([]byte("should fail"))
	assert.Error(t, err)

	mt.Close()
	time.Sleep(100 * time.Millisecond)
}

func TestMultiTransport_Close(t *testing.T) {
	defer goleak.VerifyNone(t)

	mock1 := newMockTransport("mock1")
	mock2 := newMockTransport("mock2")

	rt1 := newTestReconnectTransport(t, mock1, "sub1")
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

	err = mt.Close()
	require.NoError(t, err)

	// Verify that the internal transport is Disconnected
	assert.Equal(t, reconnect.StatusDisconnected, rt1.Status())
	assert.Equal(t, reconnect.StatusDisconnected, rt2.Status())

	// Verify that the mock transport is closed
	assert.True(t, mock1.IsClosed())
	assert.True(t, mock2.IsClosed())

	time.Sleep(200 * time.Millisecond)
}

func TestMultiTransport_OverallStatus(t *testing.T) {
	defer goleak.VerifyNone(t)

	mock1 := newMockTransport("mock1")
	mock2 := newMockTransport("mock2")

	rt1 := newTestReconnectTransport(t, mock1, "sub1")
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

	// Initial state: AllConnected
	require.Eventually(t,
		func() bool { return mt.OverallStatus() == MultiOverallStatusAllConnected },
		5*time.Second, 10*time.Millisecond,
		"overall status should be AllConnected",
	)

	closeAndWait(t, mt)
}

func TestMultiTransport_SuperConnectionID_SubConnectionID(t *testing.T) {
	defer goleak.VerifyNone(t)

	mock1 := newMockTransport("mock1")
	rt1 := newTestReconnectTransport(t, mock1, "my-sub-id")
	waitForConnected(t, rt1)

	id1 := transport.SubConnectionID("transport1")
	selector := newMockTransportSelector(id1)

	mt, err := NewTransport(TransportConfig{
		TransportMap: TransportMap{
			id1: rt1,
		},
		TransportSelector:   selector,
		Logger:              log.NewNop(),
		StatusCheckInterval: 100 * time.Millisecond,
	})
	require.NoError(t, err)

	// Verify SuperConnectionID of NegotiationParams
	params := mt.NegotiationParams()
	assert.Equal(t, transport.SuperConnectionID(testSuperConnectionID), params.SuperConnectionID)

	// Verify SubConnectionID of NegotiationParams
	assert.Equal(t, transport.SubConnectionID("my-sub-id"), params.SubConnectionID)

	// Verify that it can be accessed with the key of TransportMap
	transports := mt.Transports()
	_, exists := transports[id1]
	assert.True(t, exists)

	closeAndWait(t, mt)
}
