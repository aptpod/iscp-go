package multi_test

// . "github.com/aptpod/iscp-go/transport/multi"

// // Implementation of mock transport
// type mockTransport struct {
// 	name     transport.Name
// 	readCh   chan []byte
// 	writeCh  chan []byte
// 	closed   bool
// 	closedCh chan struct{}
// 	rxBytes  uint64
// 	txBytes  uint64
// }
//
// // Status implements multi.StatusAwareTransport.
// func (m *mockTransport) Status() reconnect.Status {
// 	return reconnect.StatusConnected
// }
//
// func newMockTransport(name transport.Name) *mockTransport {
// 	return &mockTransport{
// 		name:     name,
// 		closedCh: make(chan struct{}),
// 		readCh:   make(chan []byte, 100),
// 		writeCh:  make(chan []byte, 100),
// 	}
// }
//
// func (m *mockTransport) Read() ([]byte, error) {
// 	select {
// 	case data := <-m.readCh:
// 		m.rxBytes += uint64(len(data))
// 		return data, nil
// 	case closed := <-m.closedCh:
// 		return nil, fmt.Errorf("closed: %v", closed)
// 	}
// }
//
// func (m *mockTransport) Write(bs []byte) error {
// 	m.writeCh <- bs
// 	m.txBytes += uint64(len(bs))
// 	return nil
// }
//
// func (m *mockTransport) Close() error {
// 	if m.closed {
// 		return nil
// 	}
// 	close(m.closedCh)
// 	m.closed = true
// 	return nil
// }
//
// func (m *mockTransport) Name() transport.Name {
// 	return transport.Name(m.name)
// }
//
// func (m *mockTransport) AsUnreliable() (transport.UnreliableTransport, bool) {
// 	return nil, false
// }
//
// func (m *mockTransport) NegotiationParams() transport.NegotiationParams {
// 	return transport.NegotiationParams{
// 		TransportGroupID: "test",
// 	}
// }
//
// func (m *mockTransport) RxBytesCounterValue() uint64 {
// 	return m.rxBytes
// }
//
// func (m *mockTransport) TxBytesCounterValue() uint64 {
// 	return m.txBytes
// }
//
// // reconnect.StatusProvider を実装したモックトランスポート
// type mockReconnectTransport struct {
// 	*mockTransport
// 	status reconnect.Status
// }
//
// func newMockReconnectTransport(name transport.Name, status reconnect.Status) *mockReconnectTransport {
// 	return &mockReconnectTransport{
// 		mockTransport: newMockTransport(name),
// 		status:        status,
// 	}
// }
//
// func (m *mockReconnectTransport) Status() reconnect.Status {
// 	return m.status
// }
//
// // Implementation of Mock Scheduler
// type mockEventSubscriber struct {
// 	transportIDs <-chan transport.TransportID
// }
//
// func (m *mockEventSubscriber) Subscribe(ctx context.Context) <-chan transport.TransportID {
// 	return m.transportIDs
// }
//
// func TestNewMultiTransport(t *testing.T) {
// 	defer goleak.VerifyNone(t)
// 	t.Run("successful initialization", func(t *testing.T) {
// 		transports := multi.TransportMap{
// 			"transport1": newMockTransport("mock1"),
// 			"transport2": newMockTransport("mock2"),
// 		}
//
// 		mt, err := NewTransport(TransportConfig{
// 			TransportMap:       transports,
// 			InitialTransportID: "transport1",
// 			Logger:             log.NewNop(),
// 		})
// 		require.NoError(t, err)
// 		defer mt.Close()
//
// 		require.NoError(t, err)
// 		assert.NotNil(t, mt)
// 		assert.EqualValues(t, "transport1", mt.CurrentTransportID())
// 	})
//
// 	t.Run("invalid scheduler mode", func(t *testing.T) {
// 		_, err := NewTransport(TransportConfig{
// 			SchedulerMode: SchedulerMode(999),
// 		})
// 		assert.Error(t, err)
// 	})
// }
//
// func TestMultiTransportReadWrite(t *testing.T) {
// 	defer goleak.VerifyNone(t)
// 	mock1 := newMockTransport("mock1")
// 	mock2 := newMockTransport("mock2")
//
// 	transports := TransportMap{
// 		"transport1": mock1,
// 		"transport2": mock2,
// 	}
//
// 	ch := make(chan transport.TransportID, 1)
// 	subscriver := &mockEventSubscriber{
// 		transportIDs: ch,
// 	}
//
// 	mt, err := NewTransport(TransportConfig{
// 		TransportMap:       transports,
// 		InitialTransportID: "transport2",
// 		SchedulerMode:      SchedulerModeEvent,
// 		EventScheduler: &EventScheduler{
// 			Subscriber: subscriver,
// 		},
// 		Logger: log.NewNop(),
// 	})
// 	require.NoError(t, err)
// 	defer mt.Close()
// 	ch <- "transport1"
// 	time.Sleep(1 * time.Millisecond * 50)
//
// 	t.Run("write operation", func(t *testing.T) {
// 		testData := []byte("test data")
// 		err := mt.Write(testData)
// 		require.NoError(t, err)
//
// 		// 現在のトランスポートのwriteChからデータを確認
// 		select {
// 		case received := <-mock1.writeCh:
// 			assert.Equal(t, testData, received)
// 		case <-time.After(time.Second):
// 			t.Fatal("write timeout")
// 		}
// 	})
//
// 	t.Run("read operation", func(t *testing.T) {
// 		testData := []byte("test response")
// 		mock1.readCh <- testData
//
// 		received, err := mt.Read()
// 		require.NoError(t, err)
// 		assert.Equal(t, testData, received)
// 	})
// }
//
// func TestMultiTransportClose(t *testing.T) {
// 	defer goleak.VerifyNone(t)
// 	mock1 := newMockTransport("mock1")
// 	mock2 := newMockTransport("mock2")
//
// 	transports := TransportMap{
// 		"transport1": mock1,
// 		"transport2": mock2,
// 	}
//
// 	mt, err := NewTransport(TransportConfig{
// 		TransportMap:       transports,
// 		InitialTransportID: "transport1",
// 		Logger:             log.NewNop(),
// 	})
// 	require.NoError(t, err)
//
// 	err = mt.Close()
// 	require.NoError(t, err)
//
// 	assert.True(t, mock1.closed)
// 	assert.True(t, mock2.closed)
// }
//
// func TestMultiTransportBytesCounter(t *testing.T) {
// 	defer goleak.VerifyNone(t)
// 	mock1 := newMockTransport("mock1")
// 	mock2 := newMockTransport("mock2")
//
// 	transports := TransportMap{
// 		"transport1": mock1,
// 		"transport2": mock2,
// 	}
//
// 	mt, err := NewTransport(TransportConfig{
// 		TransportMap:       transports,
// 		InitialTransportID: "transport1",
// 		Logger:             log.NewNop(),
// 	})
// 	require.NoError(t, err)
// 	defer mt.Close()
//
// 	// データを送受信してカウンターをテスト
// 	testData := []byte("test data")
// 	err = mt.Write(testData)
// 	require.NoError(t, err)
//
// 	mock1.readCh <- testData
// 	_, err = mt.Read()
// 	require.NoError(t, err)
//
// 	assert.Equal(t, uint64(len(testData)), mt.RxBytesCounterValue())
// 	assert.Equal(t, uint64(len(testData)), mt.TxBytesCounterValue())
// }
//
// func TestMultiTransportName(t *testing.T) {
// 	defer goleak.VerifyNone(t)
// 	mock1 := newMockTransport("mock1")
// 	mock2 := newMockTransport("mock2")
//
// 	transports := TransportMap{
// 		"transport1": mock1,
// 		"transport2": mock2,
// 	}
//
// 	mt, err := NewTransport(TransportConfig{
// 		TransportMap:       transports,
// 		InitialTransportID: "transport1",
// 		SchedulerMode:      SchedulerModePolling,
// 		Logger:             log.NewNop(),
// 	})
// 	require.NoError(t, err)
// 	defer mt.Close()
//
// 	name := mt.Name()
// 	assert.Contains(t, string(name), "multiple")
// 	assert.Contains(t, string(name), "transport1-mock1")
// 	assert.Contains(t, string(name), "transport2-mock2")
// }
//
// func TestMultiTransport_Write_Fallback(t *testing.T) {
// 	defer goleak.VerifyNone(t)
//
// 	t.Run("Fallback to connected transport", func(t *testing.T) {
// 		mock1 := newMockReconnectTransport("mock1", reconnect.StatusReconnecting) // Primary, but reconnecting
// 		mock2 := newMockReconnectTransport("mock2", reconnect.StatusConnected)    // Fallback, connected
//
// 		transports := TransportMap{
// 			"transport1": mock1,
// 			"transport2": mock2,
// 		}
//
// 		mt, err := NewTransport(TransportConfig{
// 			TransportMap:       transports,
// 			InitialTransportID: "transport1", // Start with mock1
// 			Logger:             log.NewNop(),
// 		})
// 		require.NoError(t, err)
// 		defer mt.Close()
//
// 		testData := []byte("fallback test data")
// 		err = mt.Write(testData)
// 		require.NoError(t, err)
//
// 		// Check if data was written to the fallback transport (mock2)
// 		select {
// 		case received := <-mock2.writeCh:
// 			assert.Equal(t, testData, received, "Data should be written to fallback transport")
// 		case <-time.After(50 * time.Millisecond):
// 			t.Fatal("write timeout on fallback transport")
// 		}
//
// 		// Check if data was NOT written to the initial transport (mock1)
// 		select {
// 		case <-mock1.writeCh:
// 			t.Fatal("Data should not be written to the initial (reconnecting) transport")
// 		default:
// 			// Expected behavior
// 		}
// 	})
//
// 	t.Run("Fallback to reconnecting transport", func(t *testing.T) {
// 		mock1 := newMockReconnectTransport("mock1", reconnect.StatusDisconnected) // Primary, disconnected
// 		mock2 := newMockReconnectTransport("mock2", reconnect.StatusReconnecting) // Fallback, reconnecting
//
// 		transports := TransportMap{
// 			"transport1": mock1,
// 			"transport2": mock2,
// 		}
//
// 		mt, err := NewTransport(TransportConfig{
// 			TransportMap:       transports,
// 			InitialTransportID: "transport1", // Start with mock1
// 			Logger:             log.NewNop(),
// 		})
// 		require.NoError(t, err)
// 		defer mt.Close()
//
// 		testData := []byte("fallback reconnecting test data")
// 		err = mt.Write(testData)
// 		require.NoError(t, err)
//
// 		// Check if data was written to the fallback transport (mock2)
// 		select {
// 		case received := <-mock2.writeCh:
// 			assert.Equal(t, testData, received, "Data should be written to fallback (reconnecting) transport")
// 		case <-time.After(50 * time.Millisecond):
// 			t.Fatal("write timeout on fallback (reconnecting) transport")
// 		}
//
// 		// Check if data was NOT written to the initial transport (mock1)
// 		select {
// 		case <-mock1.writeCh:
// 			t.Fatal("Data should not be written to the initial (disconnected) transport")
// 		default:
// 			// Expected behavior
// 		}
// 	})
//
// 	t.Run("No available transport", func(t *testing.T) {
// 		mock1 := newMockReconnectTransport("mock1", reconnect.StatusDisconnected) // Primary, disconnected
// 		mock2 := newMockReconnectTransport("mock2", reconnect.StatusDisconnected) // Fallback, also disconnected
//
// 		transports := TransportMap{
// 			"transport1": mock1,
// 			"transport2": mock2,
// 		}
//
// 		mt, err := NewTransport(TransportConfig{
// 			TransportMap:       transports,
// 			InitialTransportID: "transport1", // Start with mock1
// 			Logger:             log.NewNop(),
// 		})
// 		require.NoError(t, err)
// 		defer mt.Close()
//
// 		testData := []byte("no transport test data")
// 		err = mt.Write(testData)
// 		// Expecting ErrAlreadyClosed because fallbackConn returns exists=false
// 		require.ErrorIs(t, err, transport.ErrAlreadyClosed, "Expected error when no transport is available")
//
// 		// Check data was NOT written to either transport
// 		select {
// 		case <-mock1.writeCh:
// 			t.Fatal("Data should not be written when no transport is available")
// 		case <-mock2.writeCh:
// 			t.Fatal("Data should not be written when no transport is available")
// 		default:
// 			// Expected behavior
// 		}
// 	})
//
// 	t.Run("No fallback needed", func(t *testing.T) {
// 		mock1 := newMockReconnectTransport("mock1", reconnect.StatusConnected) // Primary, connected
// 		mock2 := newMockReconnectTransport("mock2", reconnect.StatusConnected) // Fallback, connected
//
// 		transports := TransportMap{
// 			"transport1": mock1,
// 			"transport2": mock2,
// 		}
//
// 		mt, err := NewTransport(TransportConfig{
// 			TransportMap:       transports,
// 			InitialTransportID: "transport1", // Start with mock1
// 			Logger:             log.NewNop(),
// 		})
// 		require.NoError(t, err)
// 		defer mt.Close()
//
// 		testData := []byte("no fallback needed test data")
// 		err = mt.Write(testData)
// 		require.NoError(t, err)
//
// 		// Check if data was written to the primary transport (mock1)
// 		select {
// 		case received := <-mock1.writeCh:
// 			assert.Equal(t, testData, received, "Data should be written to primary transport")
// 		case <-time.After(50 * time.Millisecond):
// 			t.Fatal("write timeout on primary transport")
// 		}
//
// 		// Check if data was NOT written to the fallback transport (mock2)
// 		select {
// 		case <-mock2.writeCh:
// 			t.Fatal("Data should not be written to the fallback transport when primary is connected")
// 		default:
// 			// Expected behavior
// 		}
// 	})
// }
