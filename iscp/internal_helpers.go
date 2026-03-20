package iscp

import (
	"context"

	"github.com/aptpod/iscp-go/errors"
)

// waitForReconnectingは、接続状態がconnStatusReconnectingになるまで待機します。
// 再接続状態を検出したらstreamStatusResumingに遷移し、エラーを返します。
// ctxがキャンセルされた場合はnilを返します。
func waitForReconnecting(ctx context.Context, cs *connStatus, state *streamState) error {
	cs.cond.L.Lock()
	for !cs.IsWithoutLock(connStatusReconnecting) {
		select {
		case <-ctx.Done():
			cs.cond.L.Unlock()
			return nil
		default:
		}
		cs.cond.Wait()
	}
	cs.cond.L.Unlock()
	state.Swap(streamStatusResuming)
	return errors.New("unexpected disconnected")
}

// orDone wraps a channel with context cancellation support.
// When ctx is cancelled or ch is closed, the returned channel is closed.
func orDone[T any](ctx context.Context, ch <-chan T) <-chan T {
	out := make(chan T)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case v, ok := <-ch:
				if !ok {
					return
				}
				select {
				case out <- v:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}
