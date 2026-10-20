package multi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	cwebsocket "github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aptpod/iscp-go/v2/log"
	"github.com/aptpod/iscp-go/v2/transport"
	"github.com/aptpod/iscp-go/v2/transport/compress"
	. "github.com/aptpod/iscp-go/v2/transport/multi"
	"github.com/aptpod/iscp-go/v2/transport/reconnect"
	"github.com/aptpod/iscp-go/v2/transport/websocket"
)

// TestECFSelectorIntegration_NewTransport verifies that
// multi.Transport can be initialized using ECFSelector.
func TestECFSelectorIntegration_NewTransport(t *testing.T) {
	// Start a WebSocket echo server
	sv := httptest.NewServer(http.HandlerFunc(echoHandler(t)))
	t.Cleanup(sv.Close)
	svURL, err := url.Parse(sv.URL)
	require.NoError(t, err)

	// Create a reconnect.Transport
	tr, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: websocket.NewDefaultDialer(),
		DialConfig: transport.DialConfig{
			Address:           svURL.Host,
			CompressConfig:    compress.Config{},
			EncodingName:      transport.EncodingNameJSON,
			SuperConnectionID: "test-group",
		},
		MaxReconnectAttempts: 10,
		ReconnectInterval:    time.Millisecond * 100,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { tr.Close() })

	// Wait for the connection to be established
	require.Eventually(t,
		func() bool { return tr.Status() == reconnect.StatusConnected },
		time.Second, 10*time.Millisecond,
		"status should become Connected",
	)

	// Create an ECFSelector
	selector := NewECFSelector()

	// Create a multi.Transport
	transportMap := TransportMap{
		"transport1": tr,
	}

	mt, err := NewTransport(TransportConfig{
		TransportMap:      transportMap,
		TransportSelector: selector,
		Logger:            log.NewNop(),
	})
	require.NoError(t, err)
	defer mt.Close()

	// Verify that initialization completed normally
	assert.NotNil(t, mt)
}

// TestECFSelectorIntegration_MetricsUpdateLoop verifies that the metrics update loop
// of ECFSelector works correctly.
func TestECFSelectorIntegration_MetricsUpdateLoop(t *testing.T) {
	// Start a WebSocket echo server
	sv := httptest.NewServer(http.HandlerFunc(echoHandler(t)))
	t.Cleanup(sv.Close)
	svURL, err := url.Parse(sv.URL)
	require.NoError(t, err)

	// Create a reconnect.Transport
	tr, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: websocket.NewDefaultDialer(),
		DialConfig: transport.DialConfig{
			Address:           svURL.Host,
			CompressConfig:    compress.Config{},
			EncodingName:      transport.EncodingNameJSON,
			SuperConnectionID: "test-group",
		},
		MaxReconnectAttempts: 10,
		ReconnectInterval:    time.Millisecond * 100,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { tr.Close() })

	// Wait for the connection to be established
	require.Eventually(t,
		func() bool { return tr.Status() == reconnect.StatusConnected },
		time.Second, 10*time.Millisecond,
		"status should become Connected",
	)

	// Create an ECFSelector
	selector := NewECFSelector()

	// Create a multi.Transport
	transportMap := TransportMap{
		"transport1": tr,
	}

	mt, err := NewTransport(TransportConfig{
		TransportMap:      transportMap,
		TransportSelector: selector,
		Logger:            log.NewNop(),
	})
	require.NoError(t, err)
	defer mt.Close()

	// Wait for the metrics update loop to run
	time.Sleep(150 * time.Millisecond)

	// Call Get() and verify that a transport is selected
	selectedID := selector.Get(context.Background(), 1000)
	assert.Equal(t, transport.SubConnectionID("transport1"), selectedID)
}

// TestECFSelectorIntegration_MultipleTransports verifies that ECFSelector works correctly
// with a multi.Transport that has multiple transports.
func TestECFSelectorIntegration_MultipleTransports(t *testing.T) {
	// Start 2 WebSocket echo servers
	sv1 := httptest.NewServer(http.HandlerFunc(echoHandler(t)))
	t.Cleanup(sv1.Close)
	svURL1, err := url.Parse(sv1.URL)
	require.NoError(t, err)

	sv2 := httptest.NewServer(http.HandlerFunc(echoHandler(t)))
	t.Cleanup(sv2.Close)
	svURL2, err := url.Parse(sv2.URL)
	require.NoError(t, err)

	// Create 2 reconnect.Transports
	tr1, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: websocket.NewDefaultDialer(),
		DialConfig: transport.DialConfig{
			Address:           svURL1.Host,
			CompressConfig:    compress.Config{},
			EncodingName:      transport.EncodingNameJSON,
			SuperConnectionID: "test-group",
		},
		MaxReconnectAttempts: 10,
		ReconnectInterval:    time.Millisecond * 100,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { tr1.Close() })

	tr2, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: websocket.NewDefaultDialer(),
		DialConfig: transport.DialConfig{
			Address:           svURL2.Host,
			CompressConfig:    compress.Config{},
			EncodingName:      transport.EncodingNameJSON,
			SuperConnectionID: "test-group",
		},
		MaxReconnectAttempts: 10,
		ReconnectInterval:    time.Millisecond * 100,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { tr2.Close() })

	// Wait for the connections to be established
	require.Eventually(t,
		func() bool { return tr1.Status() == reconnect.StatusConnected },
		time.Second, 10*time.Millisecond,
		"transport1 status should become Connected",
	)
	require.Eventually(t,
		func() bool { return tr2.Status() == reconnect.StatusConnected },
		time.Second, 10*time.Millisecond,
		"transport2 status should become Connected",
	)

	// Create an ECFSelector
	selector := NewECFSelector()

	// Create a multi.Transport
	transportMap := TransportMap{
		"transport1": tr1,
		"transport2": tr2,
	}

	mt, err := NewTransport(TransportConfig{
		TransportMap:      transportMap,
		TransportSelector: selector,
		Logger:            log.NewNop(),
	})
	require.NoError(t, err)
	defer mt.Close()

	// Wait for the metrics update loop to run
	time.Sleep(150 * time.Millisecond)

	// Call Get() and verify that a transport is selected
	selectedID := selector.Get(context.Background(), 1000)
	assert.NotEqual(t, transport.SubConnectionID(""), selectedID)
	assert.True(t, selectedID == "transport1" || selectedID == "transport2")
}

// TestECFSelectorIntegration_WriteWithSelector verifies that data can be written
// through multi.Transport using ECFSelector.
func TestECFSelectorIntegration_WriteWithSelector(t *testing.T) {
	// Start a WebSocket echo server
	sv := httptest.NewServer(http.HandlerFunc(echoHandler(t)))
	t.Cleanup(sv.Close)
	svURL, err := url.Parse(sv.URL)
	require.NoError(t, err)

	// Create a reconnect.Transport
	tr, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: websocket.NewDefaultDialer(),
		DialConfig: transport.DialConfig{
			Address:           svURL.Host,
			CompressConfig:    compress.Config{},
			EncodingName:      transport.EncodingNameJSON,
			SuperConnectionID: "test-group",
		},
		MaxReconnectAttempts: 10,
		ReconnectInterval:    time.Millisecond * 100,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { tr.Close() })

	// Wait for the connection to be established
	require.Eventually(t,
		func() bool { return tr.Status() == reconnect.StatusConnected },
		time.Second, 10*time.Millisecond,
		"status should become Connected",
	)

	// Create an ECFSelector
	selector := NewECFSelector()

	// Create a multi.Transport
	transportMap := TransportMap{
		"transport1": tr,
	}

	mt, err := NewTransport(TransportConfig{
		TransportMap:      transportMap,
		TransportSelector: selector,
		Logger:            log.NewNop(),
	})
	require.NoError(t, err)
	defer mt.Close()

	// Wait for the metrics update loop to run
	time.Sleep(150 * time.Millisecond)

	// Write data
	testData := []byte("test message")
	err = mt.Write(testData)
	require.NoError(t, err)

	// Read the echoed data
	received, err := mt.Read()
	require.NoError(t, err)
	assert.Equal(t, testData, received)
}

// TestECFSelectorIntegration_ConcurrentAccess verifies that concurrent access
// to a multi.Transport using ECFSelector is safe.
func TestECFSelectorIntegration_ConcurrentAccess(t *testing.T) {
	// Start a WebSocket echo server
	sv := httptest.NewServer(http.HandlerFunc(echoHandler(t)))
	t.Cleanup(sv.Close)
	svURL, err := url.Parse(sv.URL)
	require.NoError(t, err)

	// Create a reconnect.Transport
	tr, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: websocket.NewDefaultDialer(),
		DialConfig: transport.DialConfig{
			Address:           svURL.Host,
			CompressConfig:    compress.Config{},
			EncodingName:      transport.EncodingNameJSON,
			SuperConnectionID: "test-group",
		},
		MaxReconnectAttempts: 10,
		ReconnectInterval:    time.Millisecond * 100,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { tr.Close() })

	// Wait for the connection to be established
	require.Eventually(t,
		func() bool { return tr.Status() == reconnect.StatusConnected },
		time.Second, 10*time.Millisecond,
		"status should become Connected",
	)

	// Create an ECFSelector
	selector := NewECFSelector()

	// Create a multi.Transport
	transportMap := TransportMap{
		"transport1": tr,
	}

	mt, err := NewTransport(TransportConfig{
		TransportMap:      transportMap,
		TransportSelector: selector,
		Logger:            log.NewNop(),
	})
	require.NoError(t, err)
	defer mt.Close()

	// Wait for the metrics update loop to run
	time.Sleep(150 * time.Millisecond)

	// Test concurrent access
	var wg sync.WaitGroup
	const numGoroutines = 10
	const numIterations = 10

	// Concurrent writes
	for range numGoroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range numIterations {
				_ = mt.Write([]byte("concurrent test"))
			}
		}()
	}

	// Concurrent selector access
	for range numGoroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range numIterations {
				_ = selector.Get(context.Background(), 1000)
			}
		}()
	}

	wg.Wait()
}

// TestECFSelectorIntegration_TransportMetricsUpdaterInterface verifies that
// ECFSelector implements the TransportMetricsUpdater interface.
func TestECFSelectorIntegration_TransportMetricsUpdaterInterface(t *testing.T) {
	selector := NewECFSelector()

	// Verify that it satisfies the TransportMetricsUpdater interface
	var _ TransportMetricsUpdater = selector

	// Verify that UpdateTransport can be called
	transportID := transport.SubConnectionID("test-transport")
	provider := &mockMetricsProvider{
		rtt:              50 * time.Millisecond,
		rttvar:           25 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000,
	}
	info := NewTransportInfo(transportID, provider)
	selector.UpdateTransport(transportID, info)

	// Verify that SetQueueSize can be called
	selector.SetQueueSize(1000)

	// Verify that the transport is registered
	selectedID := selector.Get(context.Background(), 1000)
	assert.Equal(t, transportID, selectedID)
}

// TestECFSelectorIntegration_MultiTransportSetterInterface verifies that
// ECFSelector implements the MultiTransportSetter interface.
func TestECFSelectorIntegration_MultiTransportSetterInterface(t *testing.T) {
	selector := NewECFSelector()

	// Verify that it satisfies the MultiTransportSetter interface
	var _ MultiTransportSetter = selector

	// Verify that SetMultiTransport can be called
	selector.SetMultiTransport(nil)
}

// echoHandler is the handler of the WebSocket echo server.
func echoHandler(t testing.TB) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := cwebsocket.Accept(w, r, &cwebsocket.AcceptOptions{
			Subprotocols:         []string{},
			InsecureSkipVerify:   false,
			OriginPatterns:       []string{},
			CompressionMode:      0,
			CompressionThreshold: 0,
		})
		if err != nil {
			http.Error(w, "Failed to upgrade to websocket", http.StatusInternalServerError)
			return
		}
		defer conn.CloseNow()

		for {
			messageType, message, err := conn.Read(r.Context())
			if err != nil {
				break
			}
			t.Logf("messageType: %d, message: %s", messageType, string(message))
			err = conn.Write(context.Background(), messageType, message)
			if err != nil {
				break
			}
		}
	}
}
