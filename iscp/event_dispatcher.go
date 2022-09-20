package iscp

import (
	"context"
	"sync"
)

type eventDispatcher struct {
	handler []func()
	cond    *sync.Cond
}

func newEventDispatcher() *eventDispatcher {
	return &eventDispatcher{
		handler: []func(){},
		cond:    sync.NewCond(&sync.Mutex{}),
	}
}

func (u *eventDispatcher) dispatchLoop(ctx context.Context) {
	for {
		u.cond.L.Lock()
		for len(u.handler) == 0 {
			select {
			case <-ctx.Done():
				u.cond.L.Unlock()
				return
			default:
			}
			u.cond.Wait()
		}
		handlers := make([]func(), 0, len(u.handler))
		handlers = append(handlers, u.handler...)
		u.handler = u.handler[:0]
		u.cond.L.Unlock()
		for _, h := range handlers {
			if h == nil {
				return
			}
			h()
		}
	}
}

func (u *eventDispatcher) addHandler(f func()) {
	u.cond.L.Lock()
	u.handler = append(u.handler, f)
	u.cond.Signal()
	u.cond.L.Unlock()
}
