package metrics

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/quic-go/quic-go/qlog"
	"github.com/quic-go/quic-go/qlogwriter"
)

var _ ManagedMetricsProvider = (*QUICMetricsProvider)(nil)

// QUICMetricsProvider は、quic-go の qlog イベントストリームを介してトランスポートメトリクスを取得します。
// Tracer() で返される qlogwriter.Trace ファクトリを quic.Config.Tracer に渡すことで、
// quic-go から qlog.MetricsUpdated イベントが push されます。
//
// この実装は全プラットフォーム共通であり、TCP 実装（TCPInfoProvider）と同じ
// デフォルト値挙動（値==0 ならデフォルトを返却）を提供します。
type QUICMetricsProvider struct {
	mu            sync.RWMutex
	started       bool
	stopped       bool
	smoothedRTT   time.Duration
	rttvar        time.Duration
	cwnd          uint64
	bytesInFlight uint64
}

// NewQUICMetricsProvider は、新しい QUICMetricsProvider を作成します。
// プロバイダーは Start() で開始する必要があります。
func NewQUICMetricsProvider() *QUICMetricsProvider {
	return &QUICMetricsProvider{}
}

// Start は内部状態を「開始済み」にマークします。Tracer 自体は quic-go が駆動するため、
// 実処理は行いません。多重呼出および Stop 後の呼出はエラーを返します。
func (p *QUICMetricsProvider) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return fmt.Errorf("QUICMetricsProvider already stopped, cannot restart")
	}
	if p.started {
		return fmt.Errorf("QUICMetricsProvider already started")
	}
	p.started = true
	return nil
}

// Stop は内部状態を「停止済み」にマークし、以後の RecordEvent を無視させます。
// 多重呼出は安全（冪等）です。
func (p *QUICMetricsProvider) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopped = true
	p.clearMetricsLocked()
}

func (p *QUICMetricsProvider) clearMetricsLocked() {
	p.smoothedRTT = 0
	p.rttvar = 0
	p.cwnd = 0
	p.bytesInFlight = 0
}

// RTT は平滑化 RTT を返します。未測定時は defaultRTT (100ms) を返します。
func (p *QUICMetricsProvider) RTT() time.Duration {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.smoothedRTT == 0 {
		return defaultRTT
	}
	return p.smoothedRTT
}

// RTTVar は RTT 変動を返します。未測定時は defaultRTTVar (50ms) を返します。
func (p *QUICMetricsProvider) RTTVar() time.Duration {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.rttvar == 0 {
		return defaultRTTVar
	}
	return p.rttvar
}

// CongestionWindow は輻輳ウィンドウサイズをバイト単位で返します。
// 未測定時は defaultCWND (14600 バイト) を返します。
func (p *QUICMetricsProvider) CongestionWindow() uint64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.cwnd == 0 {
		return defaultCWND
	}
	return p.cwnd
}

// BytesInFlight は送信中のバイト数を返します。
func (p *QUICMetricsProvider) BytesInFlight() uint64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.bytesInFlight
}

// Tracer は quic.Config.Tracer に渡すファクトリを返します。
// 返された関数は quic-go 接続確立時に呼ばれ、qlog イベントを受け取る Trace を返します。
func (p *QUICMetricsProvider) Tracer() func(context.Context, bool, qlogwriter.ConnectionID) qlogwriter.Trace {
	return func(_ context.Context, _ bool, _ qlogwriter.ConnectionID) qlogwriter.Trace {
		return &quicTrace{provider: p}
	}
}

type quicTrace struct {
	provider *QUICMetricsProvider
}

func (t *quicTrace) AddProducer() qlogwriter.Recorder {
	return &quicRecorder{provider: t.provider}
}

func (t *quicTrace) SupportsSchemas(schema string) bool {
	return schema == qlog.EventSchema
}

type quicRecorder struct {
	provider *QUICMetricsProvider
}

// RecordEvent は qlog.MetricsUpdated を値/ポインタの両方で受け付けます。
// Stop 後の呼出は内部で無視されます（quic-go 側で Tracer 解除 API が無いため）。
func (r *quicRecorder) RecordEvent(ev qlogwriter.Event) {
	// quic-go は MetricsUpdated をポインタで渡すのが主流 (コピー回避)。ポインタ case を先に。
	var m *qlog.MetricsUpdated
	switch e := ev.(type) {
	case *qlog.MetricsUpdated:
		m = e
	case qlog.MetricsUpdated:
		m = &e
	default:
		return
	}

	p := r.provider
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return
	}
	p.smoothedRTT = m.SmoothedRTT
	p.rttvar = m.RTTVariance
	p.cwnd = uint64(m.CongestionWindow)
	p.bytesInFlight = uint64(m.BytesInFlight)
}

// Close は quic-go がコネクション終了時に呼び出します。0 クリアしてデフォルト値挙動に戻します。
func (r *quicRecorder) Close() error {
	p := r.provider
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clearMetricsLocked()
	return nil
}
