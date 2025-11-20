package multi

import (
	"time"

	"github.com/aptpod/iscp-go/transport"
	"github.com/aptpod/iscp-go/transport/metrics"
)

// TransportInfo はECFスケジューラで使用されるトランスポートの情報を保持します。
// 各トランスポートのメトリクスとステータスを管理し、ECFアルゴリズムの不等式評価に必要な情報を提供します。
type TransportInfo struct {
	transportID       transport.TransportID
	metricsProvider   metrics.MetricsProvider
	sendingAllowed    bool
	potentiallyFailed bool
}

// NewTransportInfo は新しいTransportInfoを作成します。
//
// transportID: トランスポートの識別子
// metricsProvider: メトリクスを提供するプロバイダー
func NewTransportInfo(transportID transport.TransportID, metricsProvider metrics.MetricsProvider) *TransportInfo {
	return &TransportInfo{
		transportID:     transportID,
		metricsProvider: metricsProvider,
	}
}

// Update はメトリクスプロバイダーから最新のメトリクスを取得し、
// sendingAllowed フラグを更新します。
//
// sendingAllowed は以下の条件で true になります:
//
//	BytesInFlight < CongestionWindow
//
// これにより、送信可能な帯域幅があるかどうかを判定します。
func (p *TransportInfo) Update() {
	if p.metricsProvider == nil {
		p.sendingAllowed = false
		return
	}

	bytesInFlight := p.metricsProvider.BytesInFlight()
	cwnd := p.metricsProvider.CongestionWindow()

	p.sendingAllowed = bytesInFlight < cwnd
}

// TransportID はこのトランスポートのIDを返します。
func (p *TransportInfo) TransportID() transport.TransportID {
	return p.transportID
}

// SmoothedRTT はこのトランスポートの平滑化RTT (Smoothed RTT) を返します。
// メトリクスプロバイダーが nil の場合、デフォルト値を返します。
func (p *TransportInfo) SmoothedRTT() time.Duration {
	if p.metricsProvider == nil {
		return 100 * time.Millisecond // デフォルト値
	}
	return p.metricsProvider.RTT()
}

// MeanDeviation はこのトランスポートのRTT変動 (RTTVAR, Mean Deviation) を返します。
// メトリクスプロバイダーが nil の場合、デフォルト値を返します。
func (p *TransportInfo) MeanDeviation() time.Duration {
	if p.metricsProvider == nil {
		return 50 * time.Millisecond // デフォルト値
	}
	return p.metricsProvider.RTTVar()
}

// CongestionWindow はこのトランスポートの輻輳ウィンドウサイズ（バイト数）を返します。
// メトリクスプロバイダーが nil の場合、デフォルト値を返します。
func (p *TransportInfo) CongestionWindow() uint64 {
	if p.metricsProvider == nil {
		return 14600 // デフォルト値: 10 * MSS (1460 bytes)
	}
	return p.metricsProvider.CongestionWindow()
}

// BytesInFlight はこのトランスポートで現在送信中のバイト数を返します。
// メトリクスプロバイダーが nil の場合、0 を返します。
func (p *TransportInfo) BytesInFlight() uint64 {
	if p.metricsProvider == nil {
		return 0
	}
	return p.metricsProvider.BytesInFlight()
}

// SendingAllowed はこのトランスポートで送信が可能かどうかを返します。
// Update() メソッドで更新される値です。
func (p *TransportInfo) SendingAllowed() bool {
	return p.sendingAllowed
}

// SetSendingAllowed は sendingAllowed フラグを設定します。
// 主にテスト目的で使用されます。
func (p *TransportInfo) SetSendingAllowed(allowed bool) {
	p.sendingAllowed = allowed
}

// PotentiallyFailed はこのトランスポートが潜在的に失敗している可能性があるかどうかを返します。
func (p *TransportInfo) PotentiallyFailed() bool {
	return p.potentiallyFailed
}

// SetPotentiallyFailed は potentiallyFailed フラグを設定します。
func (p *TransportInfo) SetPotentiallyFailed(failed bool) {
	p.potentiallyFailed = failed
}
