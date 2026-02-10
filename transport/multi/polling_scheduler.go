package multi

import (
	"context"
	"sync"
	"time"

	"github.com/aptpod/iscp-go/internal/ch"
	"github.com/aptpod/iscp-go/transport"
)

// poller は内部で使用するポーリングインターフェース。
// TransportSelectorとは異なり、定期的に呼び出されることを想定している。
type poller interface {
	Get(bsSize int64) transport.TransportID
}

// PollingScheduler は定期的にpollerを呼び出してTransportIDを更新するスケジューラ。
// TransportSelectorインターフェースを実装している。
type PollingScheduler struct {
	poller             poller
	interval           time.Duration
	currentTransportID transport.TransportID
	multiTransport     *Transport
	mu                 sync.RWMutex
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

// Get は現在選択されているTransportIDを返します。
// multiTransportが設定されている場合、選択されたトランスポートが利用可能か確認し、
// 利用不可の場合は接続済みの別トランスポートにフォールバックします。
func (p *PollingScheduler) Get(bsSize int64) transport.TransportID {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.multiTransport != nil {
		return SelectAvailableTransport(p.currentTransportID, p.multiTransport.Transports())
	}
	return p.currentTransportID
}

func (p *PollingScheduler) start(ctx context.Context) {
	go p.loop(ctx)
}

func (p *PollingScheduler) loop(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for range ch.ReadOrDone(ctx, ticker.C) {
		// 定期的にpollerを呼び出してTransportIDを更新
		// 平均的なデータサイズとして0を渡す（将来的に調整可能）
		id := p.poller.Get(0)

		p.mu.Lock()
		p.currentTransportID = id
		p.mu.Unlock()
	}
}
