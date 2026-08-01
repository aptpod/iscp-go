package iscp_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	. "github.com/aptpod/iscp-go/v2/iscp"
	"github.com/aptpod/iscp-go/v2/transport"
)

// gatedReconnectDialer は transport.Dialer を実装する。Dial が呼ばれるたびに
// 新しい pipe ベースの dialer（newDialer）を生成して返すが、gateAtN 回目の
// 呼び出しだけは、到達を blocked の close で通知した後 proceed が close される
// までブロックする。「reconnect が dial の途中（wireConnMu を保持したまま）で
// 止まっている」状況を確定的に作るために使う。
type gatedReconnectDialer struct {
	gateAtN int32
	n       int32
	blocked chan struct{}
	proceed chan struct{}
	created chan *dialer
}

var _ transport.Dialer = (*gatedReconnectDialer)(nil)

func newGatedReconnectDialer(gateAtN int32) *gatedReconnectDialer {
	return &gatedReconnectDialer{
		gateAtN: gateAtN,
		blocked: make(chan struct{}),
		proceed: make(chan struct{}),
		created: make(chan *dialer, 8),
	}
}

func (g *gatedReconnectDialer) Dial(c transport.DialConfig) (transport.Transport, error) {
	n := atomic.AddInt32(&g.n, 1)
	d := newDialer(transport.NegotiationParams{})
	if n == g.gateAtN {
		close(g.blocked)
		<-g.proceed
	}
	g.created <- d
	return d, nil
}

// TestConn_再接続中のCloseで確立済み新セッションが漏れない は、reconnect が
// dial 中に Close された場合に、dial 完了後の新セッションを reconnect 自身が
// 代入前に検出して閉じること（closed 再確認）の再現テスト。
// もともとは cc174e7 が wireConnMu を TryLock 化した際に生じた回帰（諦めた
// close() が新セッションを閉じられない）の再現として書かれたもの。現在は
// dial がロック外に出て close() は素の Lock() に戻ったが、「dial 中に Close が
// 来た場合、確立された新セッションは reconnect 側の closed 再確認だけが
// 閉じられる」という構造は同じであり、この再確認の回帰テストとして引き続き
// 有効。
//
// シナリオ:
//  1. 初回接続を確立する
//  2. サーバー側（d1.srv）を閉じて wireConn.Closed() を発火させ、
//     connLifecycle.reconnect() をトリガーする
//  3. reconnect() が 2 回目の dial（ロック外）に入り、gatedReconnectDialer に
//     よって確定的にそこで止まる
//  4. その間に Conn.Close(ctx) を呼ぶ。close() はロックを直ちに取得し、
//     既に閉じられた古い wireConn（d1 由来）への Disconnect 送信が即時
//     エラーになって返る
//  5. dial のブロックを解除すると reconnect の dial が成功し、新しい
//     protocolSession（d2 由来）が確立される。close() 済みであることを
//     wireConn への代入前に検出して閉じないと、この新セッションの
//     readReliableLoop/keepAliveLoop 等の goroutine を誰も閉じずに
//     リークする
//
// goleak.VerifyNone(t) がこの新セッション由来の goroutine の有無を判定する。
func TestConn_再接続中のCloseで確立済み新セッションが漏れない(t *testing.T) {
	defer goleak.VerifyNone(t)

	g := newGatedReconnectDialer(2) // 2回目（reconnect）の Dial でゲートする
	RegisterDialer(TransportTest, func() transport.Dialer { return g })

	// 初回接続のサーバー側処理。ConnectResponse を返した後、サーバー側を
	// 閉じてクライアント側の readReliableLoop に EOF を発生させ、
	// wireConn.Closed() を発火させて reconnect をトリガーする。
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

	// reconnect が 2 回目の dial（gatedReconnectDialer によりゲートされる）
	// に到達し、そこでブロックするまで待つ。
	select {
	case <-g.blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("reconnect did not reach the gated dial within timeout")
	}

	// reconnect が dial 中にブロックしている間に Close を呼ぶ。
	closeDone := make(chan error, 1)
	go func() { closeDone <- conn.Close(context.Background()) }()

	select {
	case err := <-closeDone:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Conn.Close did not return while reconnect was blocked on dial")
	}

	// dial のブロックを解除する。新セッション（d2）が確立されるので、
	// サーバー側で ConnectRequest に応答しておく（応答がないと
	// newProtocolSession の waitForConnected がブロックしたままになる）。
	// 修正が正しく効いていれば、この新セッションは reconnect() 自身に
	// よって代入前に Close されるため、readReliableLoop 等の goroutine は
	// 一切起動されない。
	d2Done := make(chan struct{})
	go func() {
		defer close(d2Done)
		d2 := <-g.created
		mockConnectRequest(t, d2.srv)
	}()
	close(g.proceed)

	select {
	case <-d2Done:
	case <-time.After(5 * time.Second):
		t.Fatal("reconnect did not complete dialing the second (gated) transport")
	}

	// connLifecycle.run() が reconnect() のエラーを検出して終了するまでの
	// 猶予。goleak.VerifyNone は内部でリトライ付きの猶予を持つため、通常は
	// 不要だが、後続の goroutine 起動（バグがあれば readReliableLoop 等）が
	// 積み上がるタイミングを安定させるために少し待つ。
	time.Sleep(100 * time.Millisecond)
}

// TestConn_Close_再接続のdialブロック中でも待たずに返る は、Close の無期限
// ブロック対策が「タイムアウトによる救済」ではなく構造的であることの検証。
//
// reconnect が dial の途中で止まっている（gatedReconnectDialer）間に
// Close(context.Background()) を呼び、タイムアウト（disconnectSendTimeout=3s）
// に頼らず即座に返ることを確認する。dial が wireConnMu の外で行われていれば
// Close はロックを直ちに取得でき、既に閉じられた旧 wireConn への Disconnect
// 送信が即時エラーになって戻る。
func TestConn_Close_再接続のdialブロック中でも待たずに返る(t *testing.T) {
	defer goleak.VerifyNone(t)

	g := newGatedReconnectDialer(2) // 2回目（reconnect）の Dial でゲートする
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

	select {
	case <-g.blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("reconnect did not reach the gated dial within timeout")
	}

	// reconnect が dial 中（ゲートで停止中）に Close を呼ぶ。
	start := time.Now()
	closeDone := make(chan error, 1)
	go func() { closeDone <- conn.Close(context.Background()) }()

	// disconnectSendTimeout（3s）より長く待ってから経過時間を判定することで、
	// 「タイムアウトで救済されて返った」（≈3s）と「待たずに返った」（ms オーダー）
	// を区別する。失敗時も後始末（ゲート解除）に進めるよう Fatal にはしない。
	select {
	case err := <-closeDone:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		assert.Fail(t, "Conn.Close did not return while reconnect was dialing")
	}
	assert.Less(t, time.Since(start), time.Second,
		"Close should return without waiting for any timeout while dial is in progress")

	// dial のブロックを解除し、破棄されるべき新セッションの後始末
	// （reconnect 側の closed 再確認による res.Close()）を待つ。
	d2Done := make(chan struct{})
	go func() {
		defer close(d2Done)
		d2 := <-g.created
		mockConnectRequest(t, d2.srv)
	}()
	close(g.proceed)

	select {
	case <-d2Done:
	case <-time.After(5 * time.Second):
		t.Fatal("reconnect did not complete dialing the second (gated) transport")
	}
	time.Sleep(100 * time.Millisecond)
}
