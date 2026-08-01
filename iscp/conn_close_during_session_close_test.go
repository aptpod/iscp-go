package iscp_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	. "github.com/aptpod/iscp-go/v2/iscp"
	"github.com/aptpod/iscp-go/v2/transport"
)

var errDialAfterClose = errors.New("test: dial rejected after close")

// blockingCloseTransport は transport.Transport の Close をゲートするラッパー。
// 最初の Close 呼び出しだけが entered を通知して release までブロックし、
// 2 回目以降の呼び出しは下層へ素通しする。
//
// 「最初の Close = reconnect() の old.Close()」が成立する根拠: 本テストは
// v4 でハンドシェイクする（mockConnectRequestV4）ため keepAliveLoop が起動
// せず、サーバー断の teardown で transport.Close() を呼ぶ経路が存在しない。
// readReliableLoop は read エラー時に復帰するだけで transport.Close() は
// 呼ばず（呼ぶのは Disconnect メッセージ受信時のみ。本テストはサーバー側
// pipe を閉じるだけなので通らない）、切断検知は read エラー → read loop
// 復帰 → runWire の wg.Wait → defer c.cancel() → Closed() 発火の順で進む。
// その後 reconnect() の old.Close() が最初に本ラッパーへ到達する。
// close() 側の wireConn.Close() はゲート消費後なので掛からない。
//
// v3 でハンドシェイクしてはいけない: keepAliveLoop が起動し（サーバーモック
// は Pong を返さないので 1 秒でタイムアウト）、その teardown の c.Close() →
// c.transport.Close() が reconnect() より先にゲートを消費しうる。消費される
// と old.Close() は素通しになり、ロック内に戻しても FAIL しない（テストが
// 静かに無効化される）。
type blockingCloseTransport struct {
	transport.Transport
	gated   atomic.Bool
	entered chan struct{}
	release chan struct{}
}

func (b *blockingCloseTransport) Close() error {
	if b.gated.CompareAndSwap(false, true) {
		close(b.entered)
		<-b.release
	}
	return b.Transport.Close()
}

// TestConn_Close_旧セッションのcloseブロック中でも待たずに返る は、fa72f87
// （再接続時の旧セッション close を wireConnMu の外に出す）の回帰テスト。
//
// reconnect() の第 1 区間が old.Close() をロック内で行うと、下層 transport の
// Close がブロックする実装（例: reconnect.Transport の CloseWithStatus は
// 実行中の dial 完了まで返らない）では Conn.Close() が wireConnMu のロック
// 待ちで道連れになる。本テストは Close がブロックする transport を差し、
// old.Close() のブロック中でも Conn.Close() が待たずに返ることを検証する。
//
// オラクルに Conn.Close を使う理由: OpenUpstream / SendMetadata 等の公開
// 送信系はすべて c.send の状態ゲート（WaitUntilOrClosed、ctx 対応）を通り、
// 再接続中（connStatusReconnecting）は wireConnMu に到達する前に ctx で
// 抜けてしまうため、ロック保持の有無を判別できない。再接続中に wireConnMu
// を直接取りにいくのは close() 自身であり、fa72f87 が守った性質は
// 「Conn.Close() が wireConnMu のロック待ちで道連れにならないこと」。
//
// 注意: このテストは Close の所要時間の改善を保証するものではない。ここで
// Close が速く返るのは、ゲートが最初の 1 回（reconnect の old.Close()）しか
// ブロックしないというテスト構成に依存している。実世界で old.Close() が
// ブロックする理由（下層 transport の Close が実行中の dial 完了を待つ）は、
// Conn.Close() 自身が呼ぶ c.wireConn.Close()（第 2 区間の代入前なので old と
// 同一オブジェクト）にも同じだけ効くため、Close の所要時間はほぼ変わらない。
// 下層 Close の有界性は L3 の残課題で、dial への ctx 伝搬（第 2 弾）で
// 解消予定。
func TestConn_Close_旧セッションのcloseブロック中でも待たずに返る(t *testing.T) {
	defer goleak.VerifyNone(t)

	bct := &blockingCloseTransport{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	created := make(chan *dialer, 8)
	var dialCount atomic.Int32
	RegisterDialer(TransportTest, func() transport.Dialer {
		return transport.DialerFunc(func(transport.DialConfig) (transport.Transport, error) {
			if dialCount.Add(1) > 1 {
				// Close と lifecycle ctx キャンセル（state 監視 goroutine 経由で
				// 非同期）の競争により、ゲート解放後に reconnect が最後の 1 回の
				// dial に進むことがある。実 transport を返すと応答のない
				// ハンドシェイクが watchdog（30 秒）までブロックして goleak に
				// 検出されるため、即エラーで打ち切らせる。
				return nil, errDialAfterClose
			}
			d := newDialer(transport.NegotiationParams{})
			created <- d
			bct.Transport = d
			return bct, nil
		})
	})

	// 初回接続に応答した後、サーバー側を閉じて reconnect をトリガーする。
	// reconnect の第 1 区間が old.Close() → bct.Close() に到達してブロックする。
	// v4 でハンドシェイクする理由は blockingCloseTransport のコメント参照。
	srv1Done := make(chan struct{})
	go func() {
		defer close(srv1Done)
		d1 := <-created
		mockConnectRequestV4(t, d1.srv)
		_ = d1.srv.Close()
	}()

	conn, err := Connect("dummy", TransportTest)
	require.NoError(t, err)
	<-srv1Done

	select {
	case <-bct.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("reconnect did not reach old.Close() within timeout")
	}

	// old.Close() がブロックしている間に Conn.Close() を呼ぶ。fa72f87 が
	// 効いていればロックは空いており、Close は待たずに返る。
	start := time.Now()
	closeDone := make(chan error, 1)
	go func() { closeDone <- conn.Close(context.Background()) }()

	closed := false
	select {
	case err := <-closeDone:
		closed = true
		assert.NoError(t, err)
		assert.Less(t, time.Since(start), 2*time.Second,
			"Close should not wait for the blocked old.Close()")
	case <-time.After(5 * time.Second):
		assert.Fail(t, "Conn.Close did not return while old session Close was blocked")
	}

	// 後始末: ゲートを解放する。reconnect は lifecycle ctx が既に
	// キャンセル済みのため dial には進まず終了する。
	close(bct.release)
	if !closed {
		select {
		case <-closeDone:
		case <-time.After(5 * time.Second):
			t.Fatal("Close never returned even after releasing the gate")
		}
	}
	// lifecycle goroutine 終了の猶予（既存テストと同様）。
	time.Sleep(100 * time.Millisecond)
}
