package iscp_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	. "github.com/aptpod/iscp-go/v2/iscp"
	"github.com/aptpod/iscp-go/v2/transport"
)

// ctxGatedReconnectDialer は transport.ContextDialer を実装する。初回の dial は
// pipe ベースの dialer を返し、2 回目以降は ctx.Done() までブロックする。
// gatedReconnectDialer（conn_reconnect_leak_test.go）の ctx 対応版で、
// 「dial がハングするが ctx は尊重する」dialer を模擬する。
type ctxGatedReconnectDialer struct {
	n           int32
	blockedOnce sync.Once
	blocked     chan struct{}
	created     chan *dialer
}

var (
	_ transport.Dialer        = (*ctxGatedReconnectDialer)(nil)
	_ transport.ContextDialer = (*ctxGatedReconnectDialer)(nil)
)

func newCtxGatedReconnectDialer() *ctxGatedReconnectDialer {
	return &ctxGatedReconnectDialer{
		blocked: make(chan struct{}),
		created: make(chan *dialer, 8),
	}
}

func (g *ctxGatedReconnectDialer) Dial(c transport.DialConfig) (transport.Transport, error) {
	return g.DialContext(context.Background(), c)
}

func (g *ctxGatedReconnectDialer) DialContext(ctx context.Context, _ transport.DialConfig) (transport.Transport, error) {
	if atomic.AddInt32(&g.n, 1) == 1 {
		d := newDialer(transport.NegotiationParams{})
		g.created <- d
		return d, nil
	}
	g.blockedOnce.Do(func() { close(g.blocked) })
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestConn_Close_ContextDialerなら再接続dialが中断され解放される は、
// 再接続の dial がブロックしていても、Close による lifecycle ctx のキャンセルが
// dialWire 経由で dial まで伝わり、dial goroutine が自力で終了することを検証する。
//
// 第 1 弾のテスト（TestConn_Close_再接続のdialブロック中でも待たずに返る）は
// 「Close が dial を待たない」ことの検証で、ブロックした dial goroutine 自体は
// テスト側がゲートを解放するまで残っていた。本テストではゲートの解放を
// 一切行わず、goleak によって「Close だけで dial goroutine が解放される」ことを
// 検証する。これが第 2 弾（dial への ctx 伝搬)の中核的な効果。
func TestConn_Close_ContextDialerなら再接続dialが中断され解放される(t *testing.T) {
	defer goleak.VerifyNone(t)

	g := newCtxGatedReconnectDialer()
	RegisterDialer(TransportTest, func() transport.Dialer { return g })

	srv1Closed := make(chan struct{})
	go func() {
		defer close(srv1Closed)
		d1 := <-g.created
		mockConnectRequest(t, d1.srv)
		_ = d1.srv.Close()
	}()

	conn, err := Connect("dummy", TransportTest)
	require.NoError(t, err)
	<-srv1Closed

	// 再接続の dial が ctx 待ちに入るまで待つ。
	select {
	case <-g.blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("reconnect did not reach the blocking dial within timeout")
	}

	start := time.Now()
	require.NoError(t, conn.Close(context.Background()))
	assert.Less(t, time.Since(start), time.Second)

	// ゲートは解放しない。Close の cancel が dial の ctx に伝搬していれば、
	// dial goroutine は自力で終了し、goleak.VerifyNone が成立する。
}

// TestConn_Close_再接続のハンドシェイク待ちも中断される は、再接続の dial は
// 成功したがサーバーが ConnectResponse を返さない（ハンドシェイク無応答）
// 状態でも、Close による lifecycle ctx のキャンセルがハンドシェイク待ちを
// 中断し、watchdog（connectHandshakeTimeout = 30 秒）を待たずに reconnect
// goroutine が解放されることを検証する。protocolSessionConfig.Context の
// 中断経路（context.AfterFunc → transport.Close）の検証。
func TestConn_Close_再接続のハンドシェイク待ちも中断される(t *testing.T) {
	defer goleak.VerifyNone(t)

	created := make(chan *dialer, 8)
	RegisterDialer(TransportTest, func() transport.Dialer {
		return transport.DialerFunc(func(transport.DialConfig) (transport.Transport, error) {
			d := newDialer(transport.NegotiationParams{})
			created <- d
			return d, nil
		})
	})

	// 初回接続に応答した直後にサーバー側を閉じ、再接続をトリガーする。
	go func() {
		d1 := <-created
		mockConnectRequest(t, d1.srv)
		_ = d1.srv.Close()
	}()

	conn, err := Connect("dummy", TransportTest)
	require.NoError(t, err)

	// 再接続の dial 完了（2 個目の dialer 作成）を待つ。以降 reconnect は
	// ハンドシェイク（waitForConnected）待ちに入るが、ConnectResponse は
	// 返さない。
	var d2 *dialer
	select {
	case d2 = <-created:
	case <-time.After(5 * time.Second):
		t.Fatal("reconnect did not dial within timeout")
	}
	t.Cleanup(func() { _ = d2.srv.Close() })

	// reconnect が waitForConnected に入るまでの猶予。
	time.Sleep(50 * time.Millisecond)

	// ハンドシェイク待ちの最中に Close する。lifecycle ctx のキャンセルが
	// protocolSessionConfig.Context 経由で transport を閉じ、reconnect
	// goroutine は watchdog の 30 秒を待たずに終了しなければならない
	// （終了しなければ goleak が検出する）。
	start := time.Now()
	require.NoError(t, conn.Close(context.Background()))
	assert.Less(t, time.Since(start), time.Second)
}
