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
	// wireConnMu の保持は「旧セッションのポインタ読み」と「新セッションの代入」の
	// 短い区間だけに限定し、旧セッションの close と dial はロック外で行う。
	// どちらもロック内で行うとブロック時に Close() までロック待ちで道連れになる
	// ため（かつてはそれを TryLock + タイムアウトで救済していた）。close は
	// 下層 transport の実装次第でブロックしうる: reconnect.Transport の
	// CloseWithStatus は実行中の dial 完了まで返らない（transport/reconnect の
	// doc 参照）。サーバー断で再接続に入るこの経路は、まさに各 sub-connection が
	// dial ループを回している状況で呼ばれる。
	//
	// 排他の根拠: CompareAndSwapNot が与えるのは state 遷移のアトミック性
	// だけで、この関数の相互排他にはならない。この区間が安全なのは、
	// reconnect() が lifecycle goroutine（connLifecycle.run のループ）から
	// しか呼ばれず並行実行が存在しないため。wireConn への書き込みも
	// この goroutine が wireConnMu 下で行うものに限られる。
	cl.conn.wireConnMu.Lock()
	if !cl.conn.state.CompareAndSwapNot(connStatusClosed, connStatusReconnecting) {
		cl.conn.wireConnMu.Unlock()
		return errors.ErrConnectionClosed
	}
	old := cl.conn.wireConn
	cl.conn.wireConnMu.Unlock()
	// close() 側も同じ旧セッションを閉じることがあるが、protocolSession.Close は
	// ctx cancel（冪等）+ transport 側の排他で並行・複数回呼び出しに安全。
	// なお wireConn をロックなしで読む resumeAllStreams / waitForDisconnect は
	// 書き込みと同じ lifecycle goroutine で動くから成立している。この読み出しを
	// 別 goroutine へ移さないこと。
	old.Close()

	var res *protocolSession
	var resErr error
	// ctx は state が connStatusClosed になった時点でキャンセルされる
	// （run() の WithCloseStatus）。Close 後はリトライ間隔のスリープ中でも
	// 即座に打ち切られ、追加の dial 試行も行われない。dialWire に ctx を渡す
	// ため、実行中の dial・ハンドシェイクも中断される（従来型 Dialer のみ
	// dialer 内部のタイムアウトまでかかりうる）。いずれもロック外なので
	// Close を妨げない。
	retry.DoWithContext(ctx, func() (end bool) {
		cl.conn.logger.Infof(ctx, "Try reconnecting...")
		res, resErr = cl.conn.Config.dialWire(ctx)
		if resErr != nil {
			return cl.conn.state.Is(connStatusClosed)
		}
		cl.conn.logger.Infof(ctx, "Reconnected")
		return true
	})
	if res == nil {
		// dial が一度も成功しないまま打ち切られた。resErr が nil のままなのは
		// ctx キャンセルにより f が一度も呼ばれなかった場合のみ。
		if resErr == nil || cl.conn.state.Is(connStatusClosed) {
			return errors.ErrConnectionClosed
		}
		return resErr
	}
	// Close() が dial 中に呼ばれていた場合、wireConn への代入前に検出して
	// 新セッションを閉じる（検出しないと新セッションを誰も閉じずにリークする。
	// cc174e7 が TryLock 化した際に生じた回帰）。判定と代入は close() と同じ
	// wireConnMu の下で行うため、取りこぼしはない:
	//
	//   - close() の state.Swap(closed) がこの判定より前に完了していれば、
	//     ここで必ず検出して res.Close() する
	//   - 判定より後に Swap した close() は、ロック待ちの後（＝代入と Unlock の
	//     後）に必ず代入済みの新しい wireConn を読んで閉じる
	//
	// かつては close() 側に TryLock で諦める経路があり、「諦めた close() と
	// この判定〜代入の競合」というリーク窓が理論上残っていたが、dial をロック
	// 外に出して close() が素の Lock() に戻ったため、その窓は構造的に存在
	// しない。
	cl.conn.wireConnMu.Lock()
	defer cl.conn.wireConnMu.Unlock()
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
