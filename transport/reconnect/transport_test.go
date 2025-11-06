package reconnect_test

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
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
			_, isControl, _ := DecodePingPong(msg)
			if isControl {
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
			_, isControl, _ := DecodePingPong(msg)
			if isControl {
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
		conn.Write(ctx, cwebsocket.MessageBinary, EncodePing(0))

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
