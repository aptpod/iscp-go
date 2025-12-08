package reconnect_test

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"testing"
	"time"

	"github.com/aptpod/iscp-go/log"
	"github.com/aptpod/iscp-go/transport"
	"github.com/aptpod/iscp-go/transport/compress"
	. "github.com/aptpod/iscp-go/transport/reconnect"
	"github.com/aptpod/iscp-go/transport/websocket"
	_ "github.com/aptpod/iscp-go/transport/websocket/coder"
	cwebsocket "github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestClientTransportReconnect_Normal(t *testing.T) {
	sv := httptest.NewServer(http.HandlerFunc(echoHandler(t)))
	t.Cleanup(sv.Close)
	svURL, err := url.Parse(sv.URL)
	require.NoError(t, err)

	tr, err := Dial(DialConfig{
		Dialer: websocket.NewDefaultDialer(),
		DialConfig: transport.DialConfig{
			Address:        svURL.Host,
			CompressConfig: compress.Config{},
			EncodingName:   transport.EncodingNameJSON,
		},
		MaxReconnectAttempts: 10,
		ReconnectInterval:    time.Millisecond * 100,
		Logger:               log.NewStd(),
	})
	require.NoError(t, err)
	defer tr.Close()
	for range 10 {
		require.NoError(t, tr.Write([]byte("hello")))
		got, err := tr.Read()
		require.NoError(t, err)
		assert.Equal(t, []byte("hello"), got)
		time.Sleep(time.Millisecond * 100)
	}
}

func TestClientTransportReconnect_Reconnect_Write(t *testing.T) {
	sv := httptest.NewServer(http.HandlerFunc(flakeyHandler(t)))
	t.Cleanup(sv.Close)
	svURL, err := url.Parse(sv.URL)
	require.NoError(t, err)

	tr, err := Dial(DialConfig{
		Dialer: websocket.NewDefaultDialer(),
		DialConfig: transport.DialConfig{
			Address:        svURL.Host,
			CompressConfig: compress.Config{},
			EncodingName:   transport.EncodingNameJSON,
		},
		MaxReconnectAttempts: 100,
		ReconnectInterval:    time.Millisecond * 100,
		Logger:               log.NewStd(),
	})
	require.NoError(t, err)
	defer tr.Close()
	for i := range 20 {
		var buf []byte
		msg := fmt.Appendf(buf, "%d", i)
		require.NoError(t, tr.Write(msg))
		t.Logf("Send message: %s", string(msg))
	}
}

func TestClientTransportReconnect_Reconnect_ReadWrite(t *testing.T) {
	sv := httptest.NewServer(http.HandlerFunc(flakeyHandler(t)))
	t.Cleanup(sv.Close)
	svURL, err := url.Parse(sv.URL)
	require.NoError(t, err)

	tr, err := Dial(DialConfig{
		Dialer: websocket.NewDefaultDialer(),
		DialConfig: transport.DialConfig{
			Address:        svURL.Host,
			CompressConfig: compress.Config{},
			EncodingName:   transport.EncodingNameJSON,
		},
		MaxReconnectAttempts: 100,
		ReconnectInterval:    time.Millisecond * 100,
		Logger:               log.NewStd(),
	})
	require.NoError(t, err)
	defer tr.Close()

	// read loop
	readCh := make(chan []byte)
	go func() {
		defer close(readCh)
		for {
			msg, err := tr.Read()
			if err != nil {
				return
			}
			// ignore ping/pong control messages
			if _, ok, _ := TryParseControlMessage(msg); ok {
				continue
			}
			readCh <- msg
		}
	}()

	for i := range 20 {
		var buf []byte
		msg := fmt.Appendf(buf, "%d", i)
	LOOP:
		for {
			require.NoError(t, tr.Write(msg))
			t.Logf("Send message: %s", string(msg))
			select {
			case got, ok := <-readCh:
				require.True(t, ok)
				assert.Equal(t, []byte(msg), got)
				time.Sleep(time.Millisecond * 50)
				break LOOP
			case <-time.After(time.Millisecond * 100):
				continue
			}
		}
	}
}

func TestClientTransportReconnect_Reconnect_KeepAlive(t *testing.T) {
	sv := httptest.NewServer(http.HandlerFunc(flakeyHandler(t)))
	t.Cleanup(sv.Close)
	svURL, err := url.Parse(sv.URL)
	require.NoError(t, err)

	tr, err := Dial(DialConfig{
		Dialer: websocket.NewDefaultDialer(),
		DialConfig: transport.DialConfig{
			Address:        svURL.Host,
			CompressConfig: compress.Config{},
			EncodingName:   transport.EncodingNameJSON,
		},
		MaxReconnectAttempts: 100,
		ReconnectInterval:    time.Millisecond * 100,
		Logger:               log.NewStd(),
	})
	require.NoError(t, err)
	defer tr.Close()

	// read loop
	readCh := make(chan []byte)
	go func() {
		defer close(readCh)
		for {
			msg, err := tr.Read()
			if err != nil {
				return
			}
			// ignore ping/pong control messages
			if _, ok, _ := TryParseControlMessage(msg); ok {
				continue
			}

			readCh <- msg
		}
	}()

	for i := range 20 {
		var buf []byte
		msg := fmt.Appendf(buf, "%d", i)
	LOOP:
		for {
			require.NoError(t, tr.Write(msg))
			t.Logf("Send message: %s", string(msg))
			select {
			case got, ok := <-readCh:
				require.True(t, ok)
				assert.Equal(t, []byte(msg), got)
				time.Sleep(time.Millisecond * 50)
				break LOOP
			case <-time.After(time.Millisecond * 100):
				continue
			}
		}
	}
}

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

			if err = conn.Write(r.Context(), messageType, message); err != nil {
				break
			}
		}
	}
}

func flakeyHandler(t testing.TB) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if randomUnavailable() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}

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
		ctx, cancel := context.WithTimeout(r.Context(), randomDuration())
		defer cancel()

		// Send initial ping with sequence 0
		pingMsg, _ := (&PingMessage{Sequence: 0}).MarshalBinary()
		conn.Write(ctx, cwebsocket.MessageBinary, pingMsg)

		for {
			messageType, message, err := conn.Read(ctx)
			if err != nil {
				break
			}
			t.Logf("Received messageType: %d, message: %s", messageType, string(message))

			if err = conn.Write(context.Background(), messageType, message); err != nil {
				break
			}
		}
	}
}

func randomUnavailable() bool {
	return rand.Intn(3) == 0
}

func randomDuration() time.Duration {
	return time.Duration(100+rand.Intn(100)) * time.Millisecond
}

// unavailableHandler returns an HTTP handler that always returns 503 Service Unavailable.
// Used to simulate a server that is completely down.
func unavailableHandler() func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
}

// TestStatusWithFlakeyHandler verifies that Status transitions correctly
// when using flakeyHandler which randomly disconnects.
func TestStatusWithFlakeyHandler(t *testing.T) {
	// Start server with flakeyHandler
	sv := httptest.NewServer(http.HandlerFunc(flakeyHandler(t)))
	defer sv.Close()
	u, _ := url.Parse(sv.URL)

	// Dial and verify initial status is Connected
	tr, err := Dial(DialConfig{
		Dialer:               websocket.NewDefaultDialer(),
		DialConfig:           transport.DialConfig{Address: u.Host},
		MaxReconnectAttempts: 5,
		ReconnectInterval:    20 * time.Millisecond,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)
	defer tr.Close()
	assert.Equal(t, StatusConnecting, tr.Status(), "initial status should be Connecting")

	// Invoke Write multiple times to trigger reconnection
	for i := range 50 {
		err := tr.Write(fmt.Appendf([]byte{}, "%d", i))
		assert.NoError(t, err)
		time.Sleep(10 * time.Millisecond)
	}

	// Wait for status to become Reconnecting
	require.Eventually(t,
		func() bool { return tr.Status() == StatusReconnecting },
		time.Second, 10*time.Millisecond,
		"status should become Reconnecting at least once",
	)

	// Wait for status to return to Connected
	require.Eventually(t,
		func() bool { return tr.Status() == StatusConnected },
		time.Second, 10*time.Millisecond,
		"status should return to Connected after successful reconnection",
	)

	// Close should set status to Disconnected
	tr.Close()
	assert.Equal(t, StatusDisconnected, tr.Status(), "status should be Disconnected after Close")
}

// TestPingPeriodicSending verifies that Transport sends ping messages periodically.
func TestPingPeriodicSending(t *testing.T) {
	pingReceived := make(chan uint32, 10)

	// Create a WebSocket server that captures received ping messages
	sv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := cwebsocket.Accept(w, r, &cwebsocket.AcceptOptions{})
		if err != nil {
			http.Error(w, "Failed to upgrade to websocket", http.StatusInternalServerError)
			return
		}
		defer conn.CloseNow()

		for {
			messageType, message, err := conn.Read(r.Context())
			if err != nil {
				return
			}

			t.Logf("Server received messageType: %d, message: %v", messageType, message)

			// Check if it's a control message
			if msg, ok, parseErr := TryParseControlMessage(message); parseErr == nil && ok {
				if ping, isPing := msg.(*PingMessage); isPing {
					t.Logf("Server received Ping with sequence: %d", ping.Sequence)
					pingReceived <- ping.Sequence

					// Send pong response
					pongMsg, _ := (&PongMessage{Sequence: ping.Sequence}).MarshalBinary()
					if err := conn.Write(r.Context(), cwebsocket.MessageBinary, pongMsg); err != nil {
						return
					}
				}
			}
		}
	}))
	defer sv.Close()

	u, err := url.Parse(sv.URL)
	require.NoError(t, err)

	// Create Transport with short ping interval
	tr, err := Dial(DialConfig{
		Dialer: websocket.NewDefaultDialer(),
		DialConfig: transport.DialConfig{
			Address:        u.Host,
			CompressConfig: compress.Config{},
			EncodingName:   transport.EncodingNameJSON,
		},
		MaxReconnectAttempts: 5,
		ReconnectInterval:    100 * time.Millisecond,
		PingInterval:         100 * time.Millisecond, // Short interval for testing
		Logger:               log.NewStd(),
	})
	require.NoError(t, err)
	defer tr.Close()

	// Wait for Transport to be connected
	require.Eventually(t,
		func() bool { return tr.Status() == StatusConnected },
		2*time.Second, 10*time.Millisecond,
		"Transport should be connected",
	)

	// Verify that at least 3 pings are received within a reasonable time
	receivedCount := 0
	timeout := time.After(500 * time.Millisecond)

	for receivedCount < 3 {
		select {
		case seq := <-pingReceived:
			t.Logf("Test received ping notification with sequence: %d", seq)
			receivedCount++
		case <-timeout:
			t.Fatalf("Timeout: only received %d pings, expected at least 3", receivedCount)
		}
	}

	assert.GreaterOrEqual(t, receivedCount, 3, "Should receive at least 3 ping messages")
}

// TestPongAutoReply verifies that Transport automatically replies with pong when receiving ping.
func TestPongAutoReply(t *testing.T) {
	pongReceived := make(chan uint32, 10)
	serverReady := make(chan struct{})

	// Create a WebSocket server that sends ping and waits for pong
	sv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := cwebsocket.Accept(w, r, &cwebsocket.AcceptOptions{})
		if err != nil {
			http.Error(w, "Failed to upgrade to websocket", http.StatusInternalServerError)
			return
		}
		defer conn.CloseNow()

		close(serverReady)

		// Start a goroutine to read incoming messages (pongs)
		go func() {
			for {
				_, message, err := conn.Read(r.Context())
				if err != nil {
					return
				}

				t.Logf("Server received message: %v", message)

				// Check if it's a pong message
				if msg, ok, parseErr := TryParseControlMessage(message); parseErr == nil && ok {
					if pong, isPong := msg.(*PongMessage); isPong {
						t.Logf("Server received Pong with sequence: %d", pong.Sequence)
						pongReceived <- pong.Sequence
					}
				}
			}
		}()

		// Send ping messages from server
		for seq := uint32(1); seq <= 3; seq++ {
			time.Sleep(100 * time.Millisecond)
			pingMsg, _ := (&PingMessage{Sequence: seq}).MarshalBinary()
			t.Logf("Server sending Ping with sequence: %d", seq)
			if err := conn.Write(r.Context(), cwebsocket.MessageBinary, pingMsg); err != nil {
				t.Logf("Failed to send ping: %v", err)
				return
			}
		}

		// Keep connection alive
		time.Sleep(2 * time.Second)
	}))
	defer sv.Close()

	u, err := url.Parse(sv.URL)
	require.NoError(t, err)

	// Create Transport
	tr, err := Dial(DialConfig{
		Dialer: websocket.NewDefaultDialer(),
		DialConfig: transport.DialConfig{
			Address:        u.Host,
			CompressConfig: compress.Config{},
			EncodingName:   transport.EncodingNameJSON,
		},
		MaxReconnectAttempts: 5,
		ReconnectInterval:    100 * time.Millisecond,
		PingInterval:         10 * time.Second, // Long interval so it doesn't interfere
		Logger:               log.NewStd(),
	})
	require.NoError(t, err)
	defer tr.Close()

	// Wait for server to be ready
	<-serverReady

	// Wait for Transport to be connected
	require.Eventually(t,
		func() bool { return tr.Status() == StatusConnected },
		2*time.Second, 10*time.Millisecond,
		"Transport should be connected",
	)

	// Verify that Transport replies with pong for each ping
	expectedSequences := []uint32{1, 2, 3}
	for _, expectedSeq := range expectedSequences {
		select {
		case seq := <-pongReceived:
			t.Logf("Test received pong notification with sequence: %d", seq)
			assert.Equal(t, expectedSeq, seq, "Pong sequence should match ping sequence")
		case <-time.After(1 * time.Second):
			t.Fatalf("Timeout waiting for pong with sequence %d", expectedSeq)
		}
	}
}

// TestResourceLeakOnReconnect verifies that resources (goroutines, memory)
// are not leaked when reconnection attempts fail repeatedly.
// Uses table-driven tests to cover both limited and unlimited retry scenarios.
func TestResourceLeakOnReconnect(t *testing.T) {
	tests := []struct {
		name                 string
		maxReconnectAttempts int
		reconnectInterval    time.Duration
		waitDuration         time.Duration
	}{
		{
			name:                 "success: no leak with limited retry attempts",
			maxReconnectAttempts: 50,
			reconnectInterval:    10 * time.Millisecond,
			waitDuration:         750 * time.Millisecond,
		},
		{
			name:                 "success: no leak with unlimited retry attempts",
			maxReconnectAttempts: -1, // Unlimited (like production)
			reconnectInterval:    5 * time.Millisecond,
			waitDuration:         500 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer goleak.VerifyNone(t)

			sv := httptest.NewServer(http.HandlerFunc(unavailableHandler()))
			defer sv.Close()

			svURL, err := url.Parse(sv.URL)
			require.NoError(t, err)

			tr, err := Dial(DialConfig{
				Dialer: websocket.NewDefaultDialer(),
				DialConfig: transport.DialConfig{
					Address:        svURL.Host,
					CompressConfig: compress.Config{},
					EncodingName:   transport.EncodingNameJSON,
				},
				MaxReconnectAttempts: tt.maxReconnectAttempts,
				ReconnectInterval:    tt.reconnectInterval,
				Logger:               log.NewNop(),
			})
			require.NoError(t, err)

			time.Sleep(tt.waitDuration)

			tr.Close()
		})
	}
}

// TestGoroutineStabilityDuringReconnect verifies that goroutines don't grow during reconnection.
// This catches the issue where each dial attempt would create new goroutines that never get cleaned up.
func TestGoroutineStabilityDuringReconnect(t *testing.T) {
	tests := []struct {
		name                 string
		maxReconnectAttempts int
		reconnectInterval    time.Duration
		midWait              time.Duration
		lateWait             time.Duration
		maxGrowth            int
	}{
		{
			name:                 "success: goroutine count stable with unlimited retries",
			maxReconnectAttempts: -1,
			reconnectInterval:    5 * time.Millisecond,
			midWait:              200 * time.Millisecond,
			lateWait:             300 * time.Millisecond,
			maxGrowth:            5,
		},
		{
			name:                 "success: goroutine count stable with fast retries",
			maxReconnectAttempts: -1,
			reconnectInterval:    1 * time.Millisecond,
			midWait:              100 * time.Millisecond,
			lateWait:             200 * time.Millisecond,
			maxGrowth:            5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sv := httptest.NewServer(http.HandlerFunc(unavailableHandler()))
			defer sv.Close()

			svURL, err := url.Parse(sv.URL)
			require.NoError(t, err)

			tr, err := Dial(DialConfig{
				Dialer: websocket.NewDefaultDialer(),
				DialConfig: transport.DialConfig{
					Address:        svURL.Host,
					CompressConfig: compress.Config{},
					EncodingName:   transport.EncodingNameJSON,
				},
				MaxReconnectAttempts: tt.maxReconnectAttempts,
				ReconnectInterval:    tt.reconnectInterval,
				Logger:               log.NewNop(),
			})
			require.NoError(t, err)
			defer tr.Close()

			time.Sleep(tt.midWait)

			runtime.GC()
			midGoroutines := runtime.NumGoroutine()

			time.Sleep(tt.lateWait)

			runtime.GC()
			lateGoroutines := runtime.NumGoroutine()

			growth := lateGoroutines - midGoroutines
			t.Logf("Goroutine count: mid=%d, late=%d, growth=%d", midGoroutines, lateGoroutines, growth)

			assert.LessOrEqual(t, growth, tt.maxGrowth, "Goroutine count should not grow significantly during reconnect attempts")
		})
	}
}
