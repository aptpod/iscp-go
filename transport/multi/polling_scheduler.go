package multi

import (
	"context"
	"sync"
	"time"

	"github.com/aptpod/iscp-go/v2/internal/ch"
	"github.com/aptpod/iscp-go/v2/transport"
)

// poller is a polling interface used internally.
// Unlike TransportSelector, it is expected to be called periodically.
type poller interface {
	Get(ctx context.Context, bsSize int64) transport.SubConnectionID
}

// PollingScheduler は定期的にpollerを呼び出してSubConnectionIDを更新するスケジューラ。
// TransportSelectorインターフェースを実装している。
type PollingScheduler struct {
	poller                 poller
	interval               time.Duration
	currentSubConnectionID transport.SubConnectionID
	multiTransport         *Transport
	mu                     sync.RWMutex
}

type MultiTransportSetter interface {
	SetMultiTransport(*Transport)
}

// NewPollingScheduler は新しいPollingSchedulerを作成し、バックグラウンドでポーリングを開始します。
func NewPollingScheduler(ctx context.Context, p poller, interval time.Duration) *PollingScheduler {
	ps := &PollingScheduler{
		poller:   p,
		interval: interval,
	}
	ps.start(ctx)
	return ps
}

// SetMultiTransport は管理対象のマルチトランスポートへの参照を設定します。
// MultiTransportSetterインターフェースを実装しています。
func (p *PollingScheduler) SetMultiTransport(mt *Transport) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.multiTransport = mt
}

// Get は現在選択されているSubConnectionIDを返します。
// multiTransportが設定されている場合、選択されたトランスポートが利用可能か確認し、
// 利用不可の場合は接続済みの別トランスポートにフォールバックします。
func (p *PollingScheduler) Get(_ context.Context, _ int64) transport.SubConnectionID {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.multiTransport != nil {
		return SelectAvailableTransport(p.currentSubConnectionID, p.multiTransport.Transports())
	}
	return p.currentSubConnectionID
}

func (p *PollingScheduler) start(ctx context.Context) {
	go p.loop(ctx)
}

func (p *PollingScheduler) loop(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for range ch.ReadOrDone(ctx, ticker.C) {
		// Call the poller periodically to update the SubConnectionID.
		// Pass 0 as the average data size (can be tuned in the future).
		id := p.poller.Get(ctx, 0)

		p.mu.Lock()
		p.currentSubConnectionID = id
		p.mu.Unlock()
	}
}
