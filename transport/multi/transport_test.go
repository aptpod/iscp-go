package multi_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aptpod/iscp-go/log"
	"github.com/aptpod/iscp-go/transport"
	. "github.com/aptpod/iscp-go/transport/multi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// モックトランスポートの実装
type mockTransport struct {
	name     transport.Name
	readCh   chan []byte
	writeCh  chan []byte
	closed   bool
	closedCh chan struct{}
	rxBytes  uint64
	txBytes  uint64
}

func newMockTransport(name transport.Name) *mockTransport {
	return &mockTransport{
		name:     name,
		closedCh: make(chan struct{}),
		readCh:   make(chan []byte, 100),
		writeCh:  make(chan []byte, 100),
	}
}

func (m *mockTransport) Read() ([]byte, error) {
	select {
	case data := <-m.readCh:
		m.rxBytes += uint64(len(data))
		return data, nil
	case closed := <-m.closedCh:
		return nil, fmt.Errorf("closed: %v", closed)
	}
}

func (m *mockTransport) Write(bs []byte) error {
	m.writeCh <- bs
	m.txBytes += uint64(len(bs))
	return nil
}

func (m *mockTransport) Close() error {
	if m.closed {
		return nil
	}
	close(m.closedCh)
	m.closed = true
	return nil
}

func (m *mockTransport) Name() transport.Name {
	return m.name
}

func (m *mockTransport) AsUnreliable() (transport.UnreliableTransport, bool) {
	return nil, false
}

func (m *mockTransport) NegotiationParams() transport.NegotiationParams {
	return transport.NegotiationParams{
		TransportGroupID:         "test",
		TransportGroupTotalCount: 2,
	}
}

func (m *mockTransport) RxBytesCounterValue() uint64 {
	return m.rxBytes
}

func (m *mockTransport) TxBytesCounterValue() uint64 {
	return m.txBytes
}

// モックスケジューラーの実装
type mockEventSubscriber struct {
	transportIDs <-chan transport.TransportID
}

func (m *mockEventSubscriber) Subscribe(ctx context.Context) <-chan transport.TransportID {
	return m.transportIDs
}

func TestNewMultiTransport(t *testing.T) {
	defer goleak.VerifyNone(t)
	t.Run("successful initialization", func(t *testing.T) {
		transports := map[transport.TransportID]transport.Transport{
			"transport1": newMockTransport("mock1"),
			"transport2": newMockTransport("mock2"),
		}

		mt, err := NewTransport(TransportConfig{
			TransportMap:       transports,
			InitialTransportID: "transport1",
			Logger:             log.NewNop(),
		})
		require.NoError(t, err)
		defer mt.Close()

		require.NoError(t, err)
		assert.NotNil(t, mt)
		assert.EqualValues(t, "transport1", mt.CurrentTransportID())
	})

	t.Run("invalid scheduler mode", func(t *testing.T) {
		_, err := NewTransport(TransportConfig{
			SchedulerMode: SchedulerMode(999),
		})
		assert.Error(t, err)
	})
}

func TestMultiTransportReadWrite(t *testing.T) {
	defer goleak.VerifyNone(t)
	mock1 := newMockTransport("mock1")
	mock2 := newMockTransport("mock2")

	transports := map[transport.TransportID]transport.Transport{
		"transport1": mock1,
		"transport2": mock2,
	}

	ch := make(chan transport.TransportID, 1)
	subscriver := &mockEventSubscriber{
		transportIDs: ch,
	}

	mt, err := NewTransport(TransportConfig{
		TransportMap:       transports,
		InitialTransportID: "transport2",
		SchedulerMode:      SchedulerModeEvent,
		EventScheduler: &EventScheduler{
			Subscriber: subscriver,
		},
		Logger: log.NewNop(),
	})
	require.NoError(t, err)
	defer mt.Close()
	ch <- "transport1"
	time.Sleep(1 * time.Millisecond * 50)

	t.Run("write operation", func(t *testing.T) {
		testData := []byte("test data")
		err := mt.Write(testData)
		require.NoError(t, err)

		// 現在のトランスポートのwriteChからデータを確認
		select {
		case received := <-mock1.writeCh:
			assert.Equal(t, testData, received)
		case <-time.After(time.Second):
			t.Fatal("write timeout")
		}
	})

	t.Run("read operation", func(t *testing.T) {
		testData := []byte("test response")
		mock1.readCh <- testData

		received, err := mt.Read()
		require.NoError(t, err)
		assert.Equal(t, testData, received)
	})
}

func TestMultiTransportClose(t *testing.T) {
	defer goleak.VerifyNone(t)
	mock1 := newMockTransport("mock1")
	mock2 := newMockTransport("mock2")

	transports := map[transport.TransportID]transport.Transport{
		"transport1": mock1,
		"transport2": mock2,
	}

	mt, err := NewTransport(TransportConfig{
		TransportMap:       transports,
		InitialTransportID: "transport1",
		Logger:             log.NewNop(),
	})
	require.NoError(t, err)

	err = mt.Close()
	require.NoError(t, err)

	assert.True(t, mock1.closed)
	assert.True(t, mock2.closed)
}

func TestMultiTransportBytesCounter(t *testing.T) {
	defer goleak.VerifyNone(t)
	mock1 := newMockTransport("mock1")
	mock2 := newMockTransport("mock2")

	transports := map[transport.TransportID]transport.Transport{
		"transport1": mock1,
		"transport2": mock2,
	}

	mt, err := NewTransport(TransportConfig{
		TransportMap:       transports,
		InitialTransportID: "transport1",
		Logger:             log.NewNop(),
	})
	require.NoError(t, err)
	defer mt.Close()

	// データを送受信してカウンターをテスト
	testData := []byte("test data")
	err = mt.Write(testData)
	require.NoError(t, err)

	mock1.readCh <- testData
	_, err = mt.Read()
	require.NoError(t, err)

	assert.Equal(t, uint64(len(testData)), mt.RxBytesCounterValue())
	assert.Equal(t, uint64(len(testData)), mt.TxBytesCounterValue())
}

func TestMultiTransportName(t *testing.T) {
	defer goleak.VerifyNone(t)
	mock1 := newMockTransport("mock1")
	mock2 := newMockTransport("mock2")

	transports := map[transport.TransportID]transport.Transport{
		"transport1": mock1,
		"transport2": mock2,
	}

	mt, err := NewTransport(TransportConfig{
		TransportMap:       transports,
		InitialTransportID: "transport1",
		SchedulerMode:      SchedulerModePolling,
		Logger:             log.NewNop(),
	})
	require.NoError(t, err)
	defer mt.Close()

	name := mt.Name()
	assert.Contains(t, string(name), "multiple")
	assert.Contains(t, string(name), "transport1-mock1")
	assert.Contains(t, string(name), "transport2-mock2")
}
