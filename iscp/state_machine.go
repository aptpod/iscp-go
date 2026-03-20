package iscp

import "sync"

// stateMachine is a generic thread-safe state holder with condition variable support.
type stateMachine[T comparable] struct {
	mu      sync.RWMutex
	cond    *sync.Cond
	current T
}

func newStateMachine[T comparable](initial T) *stateMachine[T] {
	sm := &stateMachine[T]{current: initial}
	sm.cond = sync.NewCond(&sm.mu)
	return sm
}

func (sm *stateMachine[T]) Current() T {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.CurrentWithoutLock()
}

func (sm *stateMachine[T]) CurrentWithoutLock() T {
	return sm.current
}

func (sm *stateMachine[T]) Swap(state T) (old T) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.SwapWithoutLock(state)
}

func (sm *stateMachine[T]) SwapWithoutLock(state T) (old T) {
	old = sm.current
	sm.current = state
	sm.cond.Broadcast()
	return
}

func (sm *stateMachine[T]) CompareAndSwap(old, new T) (swapped bool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if !sm.IsWithoutLock(old) {
		return false
	}
	sm.SwapWithoutLock(new)
	return true
}

func (sm *stateMachine[T]) CompareAndSwapNot(old, new T) (swapped bool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.IsWithoutLock(old) {
		return false
	}
	sm.SwapWithoutLock(new)
	return true
}

func (sm *stateMachine[T]) Is(state T) bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.IsWithoutLock(state)
}

func (sm *stateMachine[T]) IsWithoutLock(state T) bool {
	return sm.current == state
}
