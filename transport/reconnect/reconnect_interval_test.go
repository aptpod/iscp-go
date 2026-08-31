package reconnect_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/aptpod/iscp-go/log"
	"github.com/aptpod/iscp-go/transport"
	. "github.com/aptpod/iscp-go/transport/reconnect"
	"github.com/aptpod/iscp-go/transport/websocket"
	_ "github.com/aptpod/iscp-go/transport/websocket/coder"
	"github.com/stretchr/testify/require"
)

// TestCloseDuringReconnectDoesNotBlock は、再接続リトライ中に Close を呼んでも
// reconnectInterval 分ブロックされず速やかに返ることを検証します（A8）。
//
// reconnect() は r.mu を保持したまま次の試行まで待機するため、待機が
// time.Sleep のように中断不能だと CloseWithStatus の r.mu.Lock() が
// 待機時間ぶん余分にブロックされる。
func TestCloseDuringReconnectDoesNotBlock(t *testing.T) {
	sv := httptest.NewServer(http.HandlerFunc(echoHandler(t)))
	svURL, err := url.Parse(sv.URL)
	require.NoError(t, err)

	tr, err := Dial(DialConfig{
		Dialer: websocket.NewDefaultDialer(),
		DialConfig: transport.DialConfig{
			Address: svURL.Host,
		},
		MaxReconnectAttempts: 100,
		ReconnectInterval:    2 * time.Second,
		ReadTimeout:          100 * time.Millisecond,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return tr.Status() == StatusConnected
	}, time.Second, 5*time.Millisecond, "initial connection should succeed")

	// サーバーを落として以降の再接続をすべて失敗させる。
	// CloseClientConnections で確立済みのWS接続も強制的に切断する
	// （Close だけでは Hijack 済みの接続が生き続けることがある）。
	sv.CloseClientConnections()
	sv.Close()

	// readLoop の readTimeout により reconnect() が r.mu を確保して
	// StatusReconnecting になるまで待つ
	require.Eventually(t, func() bool {
		return tr.Status() == StatusReconnecting
	}, 2*time.Second, 5*time.Millisecond, "reconnect should start")

	// reconnect ループが reconnectInterval の待機に入るのを待つ
	time.Sleep(50 * time.Millisecond)

	start := time.Now()
	err = tr.Close()
	elapsed := time.Since(start)
	require.NoError(t, err)
	require.Less(t, elapsed, time.Second,
		"Close should not block for reconnectInterval while reconnect retries are in progress, took %v", elapsed)
}
