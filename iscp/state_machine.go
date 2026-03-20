package iscp

import "sync"

// stateMachine is a generic thread-safe state holder with channel-based notification.
type stateMachine[T comparable] struct {
	mu      sync.RWMutex
	current T
	changed chan struct{} // closed on state change, then recreated
}

func newStateMachine[T comparable](initial T) *stateMachine[T] {
	return &stateMachine[T]{
		current: initial,
		changed: make(chan struct{}),
	}
}

// Changed returns a channel that is closed when the state changes.
// Callers must hold at least a read lock when capturing this channel reference,
// or call this method which acquires one.
func (sm *stateMachine[T]) Changed() <-chan struct{} {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.changed
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
	close(sm.changed)
	sm.changed = make(chan struct{})
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
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.IsWithoutLock(state)
}

func (sm *stateMachine[T]) IsWithoutLock(state T) bool {
	return sm.current == state
}
