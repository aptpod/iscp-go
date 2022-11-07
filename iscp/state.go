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
	*sync.RWMutex
	cond    *sync.Cond
	current connStatusValue
}

func newConnState() *connStatus {
	var mu sync.RWMutex
	return &connStatus{
		RWMutex: &mu,
		cond:    sync.NewCond(&mu),
	}
}

func (e *connStatus) Current() connStatusValue {
	e.RLock()
	defer e.RUnlock()
	return e.CurrentWithoutLock()
}

func (e *connStatus) CurrentWithoutLock() connStatusValue {
	return e.current
}

func (e *connStatus) Swap(state connStatusValue) (old connStatusValue) {
	e.Lock()
	defer e.Unlock()
	return e.SwapWithoutLock(state)
}

func (e *connStatus) CompareAndSwap(old, new connStatusValue) (swapped bool) {
	e.Lock()
	defer e.Unlock()
	if !e.IsWithoutLock(old) {
		return false
	}
	e.SwapWithoutLock(new)
	return true
}

func (e *connStatus) CompareAndSwapNot(old, new connStatusValue) (swapped bool) {
	e.Lock()
	defer e.Unlock()
	if e.IsWithoutLock(old) {
		return false
	}
	e.SwapWithoutLock(new)
	return true
}

func (e *connStatus) SwapWithoutLock(state connStatusValue) (old connStatusValue) {
	old = e.current
	e.current = state
	e.cond.Broadcast()
	return
}

func (e *connStatus) Is(state connStatusValue) bool {
	e.Lock()
	defer e.Unlock()
	return e.IsWithoutLock(state)
}

func (e *connStatus) IsWithoutLock(state connStatusValue) bool {
	return e.current == state
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
