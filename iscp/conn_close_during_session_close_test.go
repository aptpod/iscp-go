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
// armed が立った後の最初の Close 呼び出しだけが entered を通知して release
// までブロックし、それ以外の呼び出しは下層へ素通しする。
//
// armed が必要な理由: サーバー断を検知したセッション自身の teardown
// （readReliableLoop のエラー経路など）も transport.Close() を呼ぶため、
// 無条件に最初の Close をゲートするとそちらに消費され、狙いの reconnect の
// old.Close() がゲートに掛からない。OnDisconnected（waitForDisconnect の
// 返却時 = reconnect() 呼び出しの直前）で武装することで、teardown の Close
// （readReliableLoop → runWire の wg → cancel → Closed 発火の順序により、
// 武装時点で完了している）を素通しし、次に来る reconnect の old.Close() を
// 捕まえる。close() 側の wireConn.Close() はゲート消費後なので掛からない。
type blockingCloseTransport struct {
	transport.Transport
	armed   atomic.Bool
	gated   atomic.Bool
	entered chan struct{}
	release chan struct{}
}

func (b *blockingCloseTransport) Close() error {
	if b.armed.Load() && b.gated.CompareAndSwap(false, true) {
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
// を直接取りにいくのは close() 自身であり、fa72f87 が守った性質そのものが
// 「old.Close() のブロック中でも Close() が返る」こと。
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
	srv1Done := make(chan struct{})
	go func() {
		defer close(srv1Done)
		d1 := <-created
		mockConnectRequest(t, d1.srv)
		_ = d1.srv.Close()
	}()

	conn, err := Connect("dummy", TransportTest,
		WithConnDisconnectedEventHandler(DisconnectedEventHandlerFunc(func(*DisconnectedEvent) {
			bct.armed.Store(true)
		})))
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
