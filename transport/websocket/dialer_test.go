package websocket_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aptpod/iscp-go/v2/transport"
	. "github.com/aptpod/iscp-go/v2/transport/websocket"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startHangingServer starts a server that accepts requests but never responds to the
// handshake. The handler returns when r.Context() is closed by a client-side disconnect
// (ctx cancellation or HandshakeTimeout).
func startHangingServer(t *testing.T) string {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(s.Close)
	return strings.TrimPrefix(s.URL, "http://")
}

// TestDialer_DialContext_ConnectsSuccessfully verifies that a connection can be established
// via DialContext (coder default). It protects the happy path of the refactoring that
// delegates Dial to DialContext.
func TestDialer_DialContext_ConnectsSuccessfully(t *testing.T) {
	url, closeSrv := startEchoServer(t)
	defer closeSrv()

	d := NewDefaultDialer()
	tr, err := d.DialContext(context.Background(), transport.DialConfig{
		Address: strings.TrimPrefix(url, "http://"),
	})
	require.NoError(t, err)
	require.NoError(t, tr.Close())
}

// TestDialer_DialContext_CancelAbortsConnectionEstablishment verifies that the coder default
// DialFunc is aborted immediately by ctx cancellation against a server that does not respond
// to the handshake. The key point is that ctx takes effect before DialTimeout (30s).
func TestDialer_DialContext_CancelAbortsConnectionEstablishment(t *testing.T) {
	addr := startHangingServer(t)

	d := NewDialer(DialerConfig{DialTimeout: 30 * time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := d.DialContext(ctx, transport.DialConfig{Address: addr})
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Less(t, elapsed, 5*time.Second,
		"DialContext should abort promptly on ctx cancellation, took %v", elapsed)
}

// TestGorillaDial_CtxCancelAbortsConnectionEstablishment verifies that the gorilla DialFunc
// honors DialConfig.Context.
func TestGorillaDial_CtxCancelAbortsConnectionEstablishment(t *testing.T) {
	addr := startHangingServer(t)

	d := NewDialer(DialerConfig{
		DialFunc:    GorillaDial,
		DialTimeout: 30 * time.Second,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := d.DialContext(ctx, transport.DialConfig{Address: addr})
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Less(t, elapsed, 5*time.Second,
		"GorillaDial should abort promptly on ctx cancellation, took %v", elapsed)
}

// TestGorillaDial_DialTimeoutIsHonored verifies that the gorilla DialFunc uses
// DialConfig.DialTimeout as the upper limit instead of the HandshakeTimeout (45 seconds)
// of DefaultDialer. Previously, DialTimeout was ignored and it was always 45 seconds.
func TestGorillaDial_DialTimeoutIsHonored(t *testing.T) {
	addr := startHangingServer(t)

	d := NewDialer(DialerConfig{
		DialFunc:    GorillaDial,
		DialTimeout: 300 * time.Millisecond,
	})

	start := time.Now()
	_, err := d.Dial(transport.DialConfig{Address: addr})
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Less(t, elapsed, 5*time.Second,
		"GorillaDial should time out per DialTimeout (300ms), not gorilla's default 45s, took %v", elapsed)
}

// TestDialer_ReadWriteTimeoutPropagatesToTransport verifies that ReadTimeout/WriteTimeout of
// DialerConfig are passed down to the Transport via Dial.
// When 0, DefaultReadTimeout/DefaultWriteTimeout are used as before.
func TestDialer_ReadWriteTimeoutPropagatesToTransport(t *testing.T) {
	url, closeSrv := startEchoServer(t)
	defer closeSrv()
	address := strings.TrimPrefix(url, "http://")

	tests := []struct {
		name             string
		readTimeout      time.Duration
		writeTimeout     time.Duration
		wantReadTimeout  time.Duration
		wantWriteTimeout time.Duration
	}{
		{
			name:             "defaults are used for zero values",
			readTimeout:      0,
			writeTimeout:     0,
			wantReadTimeout:  DefaultReadTimeout,
			wantWriteTimeout: DefaultWriteTimeout,
		},
		{
			name:             "configured values are used as is",
			readTimeout:      3 * time.Second,
			writeTimeout:     500 * time.Millisecond,
			wantReadTimeout:  3 * time.Second,
			wantWriteTimeout: 500 * time.Millisecond,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewDialer(DialerConfig{
				ReadTimeout:  tt.readTimeout,
				WriteTimeout: tt.writeTimeout,
			})
			tr, err := d.Dial(transport.DialConfig{Address: address})
			require.NoError(t, err)
			t.Cleanup(func() { tr.Close() })

			wstr, ok := tr.(*Transport)
			require.True(t, ok)
			assert.Equal(t, tt.wantReadTimeout, wstr.ReadTimeout())
			assert.Equal(t, tt.wantWriteTimeout, wstr.WriteTimeout())
		})
	}
}
