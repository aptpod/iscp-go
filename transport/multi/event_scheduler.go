package multi

import (
	"context"
	"sync"

	"github.com/aptpod/iscp-go/internal/ch"
	"github.com/aptpod/iscp-go/transport"
)

// EventScheduler はイベントベースでTransportIDを更新するスケジューラ。
// TransportSelectorインターフェースを実装している。
type EventScheduler struct {
	subscriber         Subscriber
	currentTransportID transport.TransportID
	mu                 sync.RWMutex
}

type EventSchedulerFunc func(ctx context.Context) <-chan transport.TransportID

func (f EventSchedulerFunc) Subscribe(ctx context.Context) <-chan transport.TransportID {
	return f(ctx)
}

type Subscriber interface {
	Subscribe(ctx context.Context) <-chan transport.TransportID
}

// NewEventScheduler は新しいEventSchedulerを作成し、バックグラウンドでイベントの監視を開始します。
func NewEventScheduler(ctx context.Context, subscriber Subscriber) *EventScheduler {
	es := &EventScheduler{
		subscriber: subscriber,
	}
	es.start(ctx)
	return es
}

// Get は現在選択されているTransportIDを返します。
// bsSizeパラメータは使用されていません（イベントベースのため）。
func (e *EventScheduler) Get(bsSize int64) transport.TransportID {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.currentTransportID
}

func (e *EventScheduler) start(ctx context.Context) {
	go e.loop(ctx)
}

func (e *EventScheduler) loop(ctx context.Context) {
	for id := range ch.ReadOrDone(ctx, e.subscriber.Subscribe(ctx)) {
		e.mu.Lock()
		e.currentTransportID = id
		e.mu.Unlock()
	}
}
