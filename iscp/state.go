package iscp

import (
	"context"
	"sync"

	"github.com/aptpod/iscp-go/errors"
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
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	e.cond.L.Lock()
	defer e.cond.L.Unlock()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-ctx.Done()
		e.mu.Lock()
		e.cond.Broadcast()
		e.mu.Unlock()
	}()
	for status != e.current {
		if hooker != nil {
			if err := hooker(status); err != nil {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		e.cond.Wait()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	return nil
}
