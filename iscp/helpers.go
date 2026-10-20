package iscp

import (
	"context"

	"github.com/aptpod/iscp-go/v2/errors"
)

// waitForReconnecting waits until the connection state becomes connStatusReconnecting.
// When the reconnecting state is detected, it transitions to streamStatusResuming and returns an error.
// If ctx is canceled, it returns nil.
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
// v3.0.0 and later: ResumeToken is supported (sent, received, and stored)
// v2.x.x: ResumeToken is ignored (an empty string is not stored)
func resolveResumeToken(session *protocolSession, currentToken string) string {
	if session.SupportsResumeToken() {
		return currentToken
	}
	return ""
}

// orDone wraps a channel with context cancellation support.
// When ctx is canceled or ch is closed, the returned channel is closed.
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
