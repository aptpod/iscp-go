package reconnect_test

import (
	"context"
	"encoding/binary"
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
			// ignore heartbeat messages
			if len(msg) == 1 && msg[0] == byte(MessageTypeHeartbeat) {
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
			// ignore heartbeat messages
			if len(msg) == 1 && msg[0] == byte(MessageTypeHeartbeat) {
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

// TestHeartbeatPeriodicSending verifies that Transport sends heartbeat messages periodically.
func TestHeartbeatPeriodicSending(t *testing.T) {
	heartbeatReceived := make(chan struct{}, 10)

	sv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := cwebsocket.Accept(w, r, &cwebsocket.AcceptOptions{})
		if err != nil {
			http.Error(w, "Failed to upgrade to websocket", http.StatusInternalServerError)
			return
		}
		defer conn.CloseNow()

		// TransportType=ws2 の場合、websocket.Transport は UseMessageFraming=true で
		// 4バイト BigEndian 長プレフィクス付きフレーミングを使用する。
		// WebSocketメッセージ境界内に複数フレームが含まれる可能性があるため、
		// バッファリングして長さプレフィクスで分割する。
		var buf []byte
		for {
			_, message, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			buf = append(buf, message...)

			for {
				if len(buf) < 4 {
					break
				}
				msgLen := binary.BigEndian.Uint32(buf[:4])
				if len(buf) < 4+int(msgLen) {
					break
				}
				payload := buf[4 : 4+int(msgLen)]

				if len(payload) == 1 && payload[0] == byte(MessageTypeHeartbeat) {
					t.Logf("Server received heartbeat")
					heartbeatReceived <- struct{}{}
				} else if len(payload) > 0 && payload[0] == byte(MessageTypeISCP) {
					if err := conn.Write(r.Context(), cwebsocket.MessageBinary, buf[:4+int(msgLen)]); err != nil {
						return
					}
				}

				buf = buf[4+int(msgLen):]
			}
		}
	}))
	defer sv.Close()

	u, err := url.Parse(sv.URL)
	require.NoError(t, err)

	tr, err := Dial(DialConfig{
		Dialer: websocket.NewDefaultDialer(),
		DialConfig: transport.DialConfig{
			Address:        u.Host,
			CompressConfig: compress.Config{},
			EncodingName:   transport.EncodingNameJSON,
			TransportType:  transport.NegotiationNameWebSocket, // v4 を有効化
		},
		MaxReconnectAttempts: 5,
		ReconnectInterval:    100 * time.Millisecond,
		HeartbeatInterval:    1 * time.Second,
		HeartbeatTimeout:     30 * time.Second,
		Logger:               log.NewStd(),
	})
	require.NoError(t, err)
	defer tr.Close()

	require.Eventually(t,
		func() bool { return tr.Status() == StatusConnected },
		2*time.Second, 10*time.Millisecond,
		"Transport should be connected",
	)

	receivedCount := 0
	timeout := time.After(5 * time.Second)

	for receivedCount < 3 {
		select {
		case <-heartbeatReceived:
			receivedCount++
			t.Logf("Test received heartbeat %d", receivedCount)
		case <-timeout:
			t.Fatalf("Timeout: only received %d heartbeats, expected at least 3", receivedCount)
		}
	}

	assert.GreaterOrEqual(t, receivedCount, 3, "Should receive at least 3 heartbeat messages")
}

// TestMessageTypeByte_WriteAddsPrefix verifies that Write adds 0x00 prefix and Read strips it.
func TestMessageTypeByte_WriteAddsPrefix(t *testing.T) {
	// Test that Write adds 0x00 prefix to messages
	sv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := cwebsocket.Accept(w, r, &cwebsocket.AcceptOptions{})
		if err != nil {
			http.Error(w, "Failed to upgrade", http.StatusInternalServerError)
			return
		}
		defer conn.CloseNow()

		// Read the raw message from transport
		_, rawMsg, err := conn.Read(r.Context())
		if err != nil {
			return
		}

		// Verify that the first byte is 0x00 (iSCP type byte)
		if len(rawMsg) > 0 && rawMsg[0] == byte(MessageTypeISCP) {
			// Echo back the message as-is (with type prefix)
			conn.Write(r.Context(), cwebsocket.MessageBinary, rawMsg)
		}
	}))
	defer sv.Close()

	u, err := url.Parse(sv.URL)
	require.NoError(t, err)

	tr, err := Dial(DialConfig{
		Dialer: websocket.NewDefaultDialer(),
		DialConfig: transport.DialConfig{
			Address:       u.Host,
			EncodingName:  transport.EncodingNameJSON,
			TransportType: transport.NegotiationNameWebSocket, // v4 を有効化
		},
		MaxReconnectAttempts: 5,
		ReconnectInterval:    100 * time.Millisecond,
		HeartbeatInterval:    10 * time.Second, // long interval to avoid interference
		Logger:               log.NewStd(),
	})
	require.NoError(t, err)
	defer tr.Close()

	require.Eventually(t,
		func() bool { return tr.Status() == StatusConnected },
		2*time.Second, 10*time.Millisecond,
	)

	// Write a message
	testData := []byte("hello")
	require.NoError(t, tr.Write(testData))

	// Read should return the original data WITHOUT the type prefix
	got, err := tr.Read()
	require.NoError(t, err)
	assert.Equal(t, testData, got)
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

// TestWriteLoopNoReconnectOnNormalClose はサーバーが正常クローズした後、
// writeLoop が再接続を試みないことを検証するテスト。
//
// readLoop が NormalClose を検知すると r.cancel() を呼び、writeLoop の
// r.closed() チェックで再接続が抑止される。
func TestWriteLoopNoReconnectOnNormalClose(t *testing.T) {
	// サーバー: 最初の接続で1メッセージをエコーした後 StatusNormalClosure で閉じる。
	// 2回目以降の接続は 503 を返す。
	firstConn := make(chan struct{}, 1) // バッファ1で最初の接続のみ許可

	sv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case firstConn <- struct{}{}:
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}

		conn, err := cwebsocket.Accept(w, r, &cwebsocket.AcceptOptions{})
		if err != nil {
			return
		}
		defer conn.CloseNow()

		messageType, message, err := conn.Read(r.Context())
		if err != nil {
			return
		}
		if err = conn.Write(r.Context(), messageType, message); err != nil {
			return
		}

		conn.Close(cwebsocket.StatusNormalClosure, "normal close")
	}))
	t.Cleanup(sv.Close)

	svURL, err := url.Parse(sv.URL)
	require.NoError(t, err)

	reconnectAttempted := make(chan struct{}, 1)

	tr, err := Dial(DialConfig{
		Dialer: websocket.NewDefaultDialer(),
		DialConfig: transport.DialConfig{
			Address:      svURL.Host,
			EncodingName: transport.EncodingNameJSON,
		},
		MaxReconnectAttempts: 1,
		ReconnectInterval:    50 * time.Millisecond,
		Logger:               log.NewStd(),
		OnStatusChange: func(old, new Status) {
			t.Logf("Status: %v -> %v", old, new)
			if new == StatusReconnecting {
				select {
				case reconnectAttempted <- struct{}{}:
				default:
				}
			}
		},
	})
	require.NoError(t, err)
	defer tr.Close()

	require.Eventually(t,
		func() bool { return tr.Status() == StatusConnected },
		2*time.Second, 10*time.Millisecond,
		"Transport should be connected",
	)

	// エコー通信を確認
	require.NoError(t, tr.Write([]byte("hello")))
	got, err := tr.Read()
	require.NoError(t, err)
	assert.Equal(t, []byte("hello"), got)

	// readLoop が NormalClose を検知（→ r.cancel()）するまで Read
	for {
		_, err := tr.Read()
		if err != nil {
			break
		}
	}

	// NormalClose 後に Write 試行 — r.closed() = true で即座に終了するはず
	go func() {
		_ = tr.Write([]byte("after close"))
	}()

	// 再接続が発生しないことを検証
	select {
	case <-reconnectAttempted:
		t.Fatal("writeLoop should NOT attempt reconnection after NormalClose")
	case <-time.After(2 * time.Second):
		t.Log("PASS: writeLoop did not attempt reconnection after NormalClose")
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
