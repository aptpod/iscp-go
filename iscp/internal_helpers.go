package iscp

import (
	"context"

	"github.com/aptpod/iscp-go/errors"
)

// waitForReconnectingは、接続状態がconnStatusReconnectingになるまで待機します。
// 再接続状態を検出したらstreamStatusResumingに遷移し、エラーを返します。
// ctxがキャンセルされた場合はnilを返します。
func waitForReconnecting(ctx context.Context, cs *connStatus, state *streamState) error {
	for {
		cs.mu.RLock()
		if cs.IsWithoutLock(connStatusReconnecting) {
			cs.mu.RUnlock()
			state.Swap(streamStatusResuming)
			return errors.New("unexpected disconnected")
		}
		ch := cs.changed
		cs.mu.RUnlock()

		select {
		case <-ch:
			continue
		case <-ctx.Done():
			return nil
		}
	}
}

// resolveResumeToken returns the current token if the session supports resume tokens,
// otherwise returns an empty string.
// v3.0.0以降: ResumeTokenをサポート（送受信・保存する）
// v2.x.x: ResumeTokenを無視（空文字列で保存しない）
func resolveResumeToken(session *protocolSession, currentToken string) string {
	if session.SupportsResumeToken() {
		return currentToken
	}
	return ""
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
