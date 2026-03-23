package iscp

import (
	"context"
)

type eventDispatcher struct {
	ch chan func()
}

func newEventDispatcher() *eventDispatcher {
	return &eventDispatcher{
		ch: make(chan func(), 64),
	}
}

func (u *eventDispatcher) dispatchLoop(ctx context.Context) {
	for {
		select {
		case h := <-u.ch:
			if h == nil {
				return
			}
			h()
		case <-ctx.Done():
			return
		}
	}
}

func (u *eventDispatcher) addHandler(f func()) {
	select {
	case u.ch <- f:
	default:
		// buffer full, drop
	}
}
