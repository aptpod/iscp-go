package iscp

import (
	"sync"
)

type streamStatus uint8

const (
	_ streamStatus = iota
	streamStatusConnected
	streamStatusResuming
	streamStatusDraining
)

type streamState struct {
	*sync.RWMutex
	cond    *sync.Cond
	current streamStatus
}

func newStreamState() *streamState {
	var mu sync.RWMutex
	return &streamState{
		RWMutex: &mu,
		cond:    sync.NewCond(&mu),
		current: streamStatusConnected,
	}
}

func (e *streamState) Current() streamStatus {
	e.RLock()
	defer e.RUnlock()
	return e.CurrentWithoutLock()
}

func (e *streamState) CurrentWithoutLock() streamStatus {
	return e.current
}

func (e *streamState) Swap(status streamStatus) (old streamStatus) {
	e.Lock()
	defer e.Unlock()
	return e.SwapWithoutLock(status)
}

func (e *streamState) CompareAndSwap(old, new streamStatus) (swapped bool) {
	e.Lock()
	defer e.Unlock()
	if e.IsWithoutLock(old) {
		e.SwapWithoutLock(new)
		return true
	}
	return false
}

func (e *streamState) CompareAndSwapNot(old, new streamStatus) (swapped bool) {
	e.Lock()
	defer e.Unlock()
	if !e.IsWithoutLock(old) {
		e.SwapWithoutLock(new)
		return true
	}
	return false
}

func (e *streamState) SwapWithoutLock(state streamStatus) (old streamStatus) {
	old = e.current
	e.current = state
	e.cond.Broadcast()
	return
}

func (e *streamState) Is(state streamStatus) bool {
	e.Lock()
	defer e.Unlock()
	return e.IsWithoutLock(state)
}

func (e *streamState) IsWithoutLock(state streamStatus) bool {
	return e.current == state
}
