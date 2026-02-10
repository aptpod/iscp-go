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
	multiTransport     *Transport
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

// SetMultiTransport は管理対象のマルチトランスポートへの参照を設定します。
// MultiTransportSetterインターフェースを実装しています。
func (e *EventScheduler) SetMultiTransport(mt *Transport) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.multiTransport = mt
}

// Get は現在選択されているTransportIDを返します。
// multiTransportが設定されている場合、選択されたトランスポートが利用可能か確認し、
// 利用不可の場合は接続済みの別トランスポートにフォールバックします。
func (e *EventScheduler) Get(bsSize int64) transport.TransportID {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.multiTransport != nil {
		return SelectAvailableTransport(e.currentTransportID, e.multiTransport.Transports())
	}
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
