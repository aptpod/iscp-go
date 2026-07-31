package iscp

import (
	"context"
	"fmt"

	"github.com/aptpod/iscp-go/v2/errors"
	"github.com/aptpod/iscp-go/v2/internal/retry"
)

// connLifecycle は接続のライフサイクル（切断検知→再接続→全ストリーム resume）を管理する。
// ConnectWithConfig から goroutine として起動される。
//
// Resume シーケンス:
//  1. waitForDisconnect() — wireConn の切断または state 変化を待機
//  2. reconnect()        — トランスポートを再接続し wireConn を更新
//  3. resumeAllStreams()  — 全 upstream/downstream の resume() を呼び出し
//  4. state → connStatusConnected（ストリーム goroutine の再開を許可）
//  5. ReconnectedEventHandler 通知
//  6. ループ先頭へ戻る
type connLifecycle struct {
	conn *Conn
}

func (cl *connLifecycle) run(ctx context.Context) {
	for {
		ctx, cancel := cl.conn.state.WithCloseStatus(ctx)
		go func() {
			cl.conn.eventDispatcher.dispatchLoop(ctx)
		}()

		if err := cl.waitForDisconnect(ctx); err != nil {
			if err := cl.reconnect(ctx); err != nil {
				if errors.Is(err, errors.ErrConnectionClosed) {
					cl.conn.logger.Warnf(ctx, "failed to reconnect: %+v", err)
					cancel()
					return
				}
				cl.conn.logger.Errorf(ctx, "failed to reconnect: %+v", err)
				cancel()
				return
			}
			cl.resumeAllStreams(ctx)
			// resumeAllStreams 完了後に connected へ遷移。
			// ストリーム goroutine は WaitUntil(connStatusConnected) でブロックされており、
			// この遷移後に u.run()/down.run() を再開する。
			if !cl.conn.state.CompareAndSwap(connStatusReconnecting, connStatusConnected) {
				// Close() が reconnect 中または resumeAllStreams 中に呼ばれた
				cancel()
				return
			}
			cl.conn.Config.ReconnectedEventHandler.OnReconnected(&ReconnectedEvent{
				Config: cl.conn.Config,
			})
			cancel()
			continue
		}
		cancel()
		return
	}
}

// waitForDisconnect は wireConn の切断または明示的なクローズを待機する。
// 切断検出時にエラーを返すことで reconnect シーケンスを開始させる。
func (cl *connLifecycle) waitForDisconnect(ctx context.Context) error {
	defer cl.conn.Config.DisconnectedEventHandler.OnDisconnected(&DisconnectedEvent{
		Config: cl.conn.Config,
	})
	for {
		changed := cl.conn.state.Changed()
		select {
		case <-ctx.Done():
			return nil
		case <-cl.conn.wireConn.Closed():
			if cl.conn.state.Is(connStatusClosed) {
				return nil
			}
			return fmt.Errorf("unexpected disconnect: %w", errors.New("unexpected disconnected"))
		case <-changed:
			if cl.conn.state.Is(connStatusClosed) {
				return nil
			}
			if cl.conn.state.Is(connStatusReconnecting) {
				return fmt.Errorf("unexpected disconnect: %w", errors.New("unexpected transport closed"))
			}
		}
	}
}

// reconnect はトランスポートを再接続し wireConn を更新する。
// state を connStatusConnected に遷移させない — 遷移は run() で
// resumeAllStreams() 完了後に行い、ストリーム goroutine との
// レースを防ぐ。
func (cl *connLifecycle) reconnect(ctx context.Context) error {
	cl.conn.wireConnMu.Lock()
	defer cl.conn.wireConnMu.Unlock()
	if !cl.conn.state.CompareAndSwapNot(connStatusClosed, connStatusReconnecting) {
		return errors.ErrConnectionClosed
	}
	cl.conn.wireConn.Close()

	var res *protocolSession
	var resErr error
	retry.Do(func() (end bool) {
		cl.conn.logger.Infof(ctx, "Try reconnecting...")
		res, resErr = cl.conn.Config.dialWire()
		if resErr != nil {
			return cl.conn.state.Is(connStatusClosed)
		}
		cl.conn.logger.Infof(ctx, "Reconnected")
		return true
	})
	if err := resErr; err != nil {
		if cl.conn.state.Is(connStatusClosed) {
			return errors.ErrConnectionClosed
		}
		return resErr
	}
	// Close() が reconnect 中（dial 待ち）に呼ばれていた場合、wireConn への
	// 代入前に検出して新セッションを閉じる。代入後に検出する実装だと、
	// close() が wireConnMu の取得に失敗して古い wireConn だけを閉じて
	// 戻った後にここで代入してしまい、新セッションを誰も閉じずにリークする
	// （cc174e7 が TryLock 化した際に生じた回帰）。
	//
	// このチェックにも理論上の窓は残る。close() は state.Swap を
	// lockWireConnOrTimeout より必ず先に行うため、dial 中（Lock 保持中）に
	// close() が来たケースは、Swap がこのチェックより前に完了していれば必ず
	// ここで検出され res.Close() される。
	//
	// 窓が残るのは「このチェックを通過した直後から代入まで」の一瞬だけである。
	// 諦めた close() は事前にスナップショットを持たず、その場で c.wireConn を
	// 読む（iscp/conn.go:640-643）。close() がロックを取れた場合は必ず
	// reconnect の Unlock（＝代入より後）以降になるため、その場合は常に
	// 代入済みの新しい wireConn を読んで閉じる。つまりリークするのは
	// 「ロックを取れずに諦めた」場合だけで、窓は lockWireConnOrTimeout
	// （iscp/conn.go:612-632）が false を返す 2 経路のどちらでも、
	// このチェックと代入の間（間には何もない）に限られる:
	//
	//   - timer 経路（close(ctx, ...) の ctx がまだ有効）: disconnectSendTimeout
	//     （3秒）のタイマーで初めて諦める。reconnect が state チェックと代入の
	//     間（隣接する 2 行の間）で 3 秒以上デスケジュールされ続ける必要があり、
	//     致命的な GC 停止や OS レベルの長時間プリエンプションでしか起こらない。
	//   - ctx 経路（close(ctx, ...) の ctx が既にキャンセル済み/期限切れ）:
	//     lockWireConnOrTimeout の select は <-ctx.Done() でも即 false を返す
	//     （iscp/conn.go:628-629、実測 ~40µs）ため、3 秒の下限は成立しない。
	//     この経路で必要なのは、reconnect が state チェックと代入の間で
	//     デスケジュールされ続けることだけで、窓は µs〜ms オーダーと timer 経路
	//     よりはるかに短い。
	//
	// （conn_reconnect_leak_test.go の再現テストは context.Background() を渡す
	// ため timer 経路のみを踏む。ctx 経路は本番コードで到達可能だが専用の
	// 再現テストはまだない。）
	//
	// この窓を完全に閉じるには、reconnect 側が代入の後にもう一度 closed を
	// 確認して res を閉じる必要がある。atomic.Pointer 化だけでは閉じない
	// （atomic.Pointer が保証するのは読み出しの同期だけで、「読み出しの後に
	// Store が来ない」ことは保証しない。close() 側が先に立てるのも state で
	// あって wireConn ではないため、チェックと代入を単一の CAS にする、という
	// 発想自体が対象を取り違えている）。代入後チェックを追加しない理由は、
	// 「代入済みだが runWire 未起動」の区間が新たに生まれ別途の解析が必要に
	// なるため。ここでは最小差分を優先し、本 MR では見送って別途対応とする。
	if cl.conn.state.Is(connStatusClosed) {
		res.Close()
		return errors.ErrConnectionClosed
	}
	cl.conn.wireConn = res
	cl.conn.setE2ECallbacks(res)
	// setE2ECallbacks 完了後に起動する（ConnectWithConfig と同じ理由。
	// newProtocolSession のコメント参照）。
	go res.runWire()
	return nil
}

// resumeAllStreams は全 upstream/downstream の resume() を呼び出す。
// 個別の resume 失敗はログ出力して続行する（現行の各ストリーム goroutine ループと同じ挙動）。
func (cl *connLifecycle) resumeAllStreams(ctx context.Context) {
	// ロック範囲をスナップショット取得のみに縮小。
	// resume() はネットワーク I/O を含むため、ロック保持中に呼ぶと
	// Close() → saveAndClearAllUpstreams() がブロックされる。
	cl.conn.upstreamMu.Lock()
	upstreams := make([]*Upstream, 0, len(cl.conn.upstreams))
	for u := range cl.conn.upstreams {
		upstreams = append(upstreams, u)
	}
	cl.conn.upstreamMu.Unlock()

	for _, u := range upstreams {
		if err := u.resume(cl.conn.wireConn); err != nil {
			cl.conn.logger.Errorf(ctx, "failed to resume upstream [%s]: %+v", u.ID, err)
		} else {
			cl.conn.logger.Infof(ctx, "Succeeded in resuming upstream %v", u.ID.String())
		}
	}

	cl.conn.downstreamMu.Lock()
	downstreams := make([]*Downstream, 0, len(cl.conn.downstreams))
	for d := range cl.conn.downstreams {
		downstreams = append(downstreams, d)
	}
	cl.conn.downstreamMu.Unlock()

	for _, d := range downstreams {
		if err := d.resume(cl.conn); err != nil {
			cl.conn.logger.Errorf(ctx, "failed to resume downstream [%s]: %+v", d.ID, err)
		} else {
			cl.conn.logger.Infof(ctx, "Succeeded in resuming downstream [%v]", d.ID)
		}
	}
}
