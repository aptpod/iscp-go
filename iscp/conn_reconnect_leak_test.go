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
	"github.com/aptpod/iscp-go/v2/message"
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

// tryMockConnectRequest は srv 側でハンドシェイクへの応答を試みる。
// Close 済みの Conn では lifecycle ctx の中断（protocolSessionConfig.Context）
// によりハンドシェイクが途中で打ち切られるため、read/write の失敗は正常系と
// して無視する（mockConnectRequest の require なし版）。
func tryMockConnectRequest(srv *transport.MessageTransport) {
	if _, err := srv.ReadMessage(); err != nil {
		return
	}
	_ = srv.WriteMessage(&message.ConnectResponse{
		RequestID:       0,
		ProtocolVersion: "3.0.0",
		ResultCode:      message.ResultCodeSucceeded,
		ResultString:    "",
		ExtensionFields: &message.ConnectResponseExtensionFields{},
	})
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
// dial 中に Close された場合に、dial 完了後のセッション（の材料）を誰も
// 閉じずにリークしないことの再現テスト。
// もともとは cc174e7 が wireConnMu を TryLock 化した際に生じた回帰（諦めた
// close() が新セッションを閉じられない）の再現として書かれたもの。後始末の
// 主体は実装の進化で変わっている:
//   - 第 1 弾（dial のロック外化）: dial 完了後にセッションが確立され、
//     reconnect の closed 再確認（代入前）が res.Close() する
//   - 第 2 弾（dial への ctx 伝搬）: Close 済みの lifecycle ctx が
//     ハンドシェイクを中断するため、dialWire 自身がエラー経路で transport を
//     閉じ、セッションは確立まで至らない。closed 再確認は「dialWire 成功と
//     ロック取得の間に Close が入る」残りの競合窓のガードとして残る
//
// 本テストは「dial 中に Close → dial 完了」のシナリオでリークが起きない
// ことを、後始末の主体を固定せずに検証する。
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
//  5. dial のブロックを解除する。dial は成功するが、Close 済み ctx により
//     ハンドシェイクが中断され、dialWire が transport（d2 由来）を閉じる
//
// goleak.VerifyNone(t) が d2 由来の goroutine の有無を判定する。
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

	// dial のブロックを解除する。サーバー側では応答を試みるが、Close 済み
	// ctx がハンドシェイクを中断するため途中で失敗するのが正常系
	// （tryMockConnectRequest はエラーを無視する）。どの段階まで進んでも、
	// d2 由来の goroutine が残らないことは末尾の goleak が検証する。
	d2Done := make(chan struct{})
	go func() {
		defer close(d2Done)
		d2 := <-g.created
		tryMockConnectRequest(d2.srv)
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

	// dial のブロックを解除し、破棄されるべき transport の後始末（Close 済み
	// ctx によるハンドシェイク中断 → dialWire のエラー経路での Close）を待つ。
	// サーバー側の応答は中断により失敗するのが正常系。
	d2Done := make(chan struct{})
	go func() {
		defer close(d2Done)
		d2 := <-g.created
		tryMockConnectRequest(d2.srv)
	}()
	close(g.proceed)

	select {
	case <-d2Done:
	case <-time.After(5 * time.Second):
		t.Fatal("reconnect did not complete dialing the second (gated) transport")
	}
	time.Sleep(100 * time.Millisecond)
}
