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

// startHangingServer は、リクエストを受け付けるがハンドシェイクに一切応答
// しないサーバーを起動する。クライアント側の切断（ctx キャンセルや
// HandshakeTimeout）で r.Context() が閉じられるとハンドラーは戻る。
func startHangingServer(t *testing.T) string {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(s.Close)
	return strings.TrimPrefix(s.URL, "http://")
}

// TestDialer_DialContext_正常に接続できる は、DialContext 経由（coder 既定）で
// 接続が確立できることを検証する。Dial → DialContext への委譲リファクタの
// ハッピーパス保護。
func TestDialer_DialContext_正常に接続できる(t *testing.T) {
	url, closeSrv := startEchoServer(t)
	defer closeSrv()

	d := NewDefaultDialer()
	tr, err := d.DialContext(context.Background(), transport.DialConfig{
		Address: strings.TrimPrefix(url, "http://"),
	})
	require.NoError(t, err)
	require.NoError(t, tr.Close())
}

// TestDialer_DialContext_キャンセルで接続確立が中断される は、coder 既定の
// DialFunc がハンドシェイク無応答のサーバーに対して ctx キャンセルで即座に
// 中断されることを検証する。DialTimeout（30s）より先に ctx が効くことが要点。
func TestDialer_DialContext_キャンセルで接続確立が中断される(t *testing.T) {
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

// TestGorillaDial_ctxキャンセルで接続確立が中断される は、gorilla DialFunc が
// DialConfig.Context を尊重することを検証する。
func TestGorillaDial_ctxキャンセルで接続確立が中断される(t *testing.T) {
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

// TestGorillaDial_DialTimeoutが尊重される は、gorilla DialFunc が
// DefaultDialer の HandshakeTimeout（45 秒）ではなく DialConfig.DialTimeout を
// 上限とすることを検証する。従来は DialTimeout を無視して常に 45 秒だった。
func TestGorillaDial_DialTimeoutが尊重される(t *testing.T) {
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
