package iscp

import (
	"context"
	"sync"

	"github.com/aptpod/iscp-go/errors"
)

type connStatus uint8

const (
	connStatusConnected connStatus = iota
	connStatusReconnecting
	connStatusClosed
)

type connState struct {
	*sync.RWMutex
	cond    *sync.Cond
	current connStatus
}

func newConnState() *connState {
	var mu sync.RWMutex
	return &connState{
		RWMutex: &mu,
		cond:    sync.NewCond(&mu),
	}
}

func (e *connState) Current() connStatus {
	e.RLock()
	defer e.RUnlock()
	return e.CurrentWithoutLock()
}

func (e *connState) CurrentWithoutLock() connStatus {
	return e.current
}

func (e *connState) Swap(state connStatus) (old connStatus) {
	e.Lock()
	defer e.Unlock()
	return e.SwapWithoutLock(state)
}

func (e *connState) CompareAndSwap(old, new connStatus) (swapped bool) {
	e.Lock()
	defer e.Unlock()
	if !e.IsWithoutLock(old) {
		return false
	}
	e.SwapWithoutLock(new)
	return true
}

func (e *connState) CompareAndSwapNot(old, new connStatus) (swapped bool) {
	e.Lock()
	defer e.Unlock()
	if e.IsWithoutLock(old) {
		return false
	}
	e.SwapWithoutLock(new)
	return true
}

func (e *connState) SwapWithoutLock(state connStatus) (old connStatus) {
	old = e.current
	e.current = state
	e.cond.Broadcast()
	return
}

func (e *connState) Is(state connStatus) bool {
	e.Lock()
	defer e.Unlock()
	return e.IsWithoutLock(state)
}

func (e *connState) IsWithoutLock(state connStatus) bool {
	return e.current == state
}

func (e *connState) WithCloseStatus(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		defer cancel()
		e.waitUntil(ctx, connStatusClosed, nil)
	}()
	return ctx, cancel
}

func (e *connState) WaitUntil(ctx context.Context, status connStatus) error {
	return e.waitUntil(ctx, status, nil)
}

func (e *connState) WaitUntilOrClosed(ctx context.Context, status connStatus) error {
	return e.waitUntil(ctx, status, func(current connStatus) error {
		if current == connStatusClosed {
			return errors.ErrConnectionClosed
		}
		return nil
	})
}

func (e *connState) waitUntil(ctx context.Context, status connStatus, hooker func(current connStatus) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	e.cond.L.Lock()
	defer e.cond.L.Unlock()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-ctx.Done()
		e.Lock()
		e.cond.Broadcast()
		e.Unlock()
	}()
	go func() {
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
