package iscp

import (
	"context"

	"github.com/aptpod/iscp-go/v2/errors"
)

type connStatusValue uint8

const (
	connStatusConnected connStatusValue = iota
	connStatusReconnecting
	connStatusClosed
)

type connStatus struct {
	*stateMachine[connStatusValue]
}

func newConnState() *connStatus {
	return &connStatus{
		stateMachine: newStateMachine(connStatusConnected),
	}
}

func (e *connStatus) WithCloseStatus(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		defer cancel()
		e.waitUntil(ctx, connStatusClosed, nil)
	}()
	return ctx, cancel
}

func (e *connStatus) WaitUntil(ctx context.Context, status connStatusValue) error {
	return e.waitUntil(ctx, status, nil)
}

func (e *connStatus) WaitUntilOrClosed(ctx context.Context, status connStatusValue) error {
	return e.waitUntil(ctx, status, func(current connStatusValue) error {
		if current == connStatusClosed {
			return errors.ErrConnectionClosed
		}
		return nil
	})
}

func (e *connStatus) waitUntil(ctx context.Context, status connStatusValue, hooker func(current connStatusValue) error) error {
	for {
		e.mu.RLock()
		if e.current == status {
			e.mu.RUnlock()
			return nil
		}
		if hooker != nil {
			if err := hooker(e.current); err != nil {
				e.mu.RUnlock()
				return err
			}
		}
		ch := e.changed
		e.mu.RUnlock()

		select {
		case <-ch:
			continue
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
