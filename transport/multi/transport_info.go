package multi

import (
	"time"

	"github.com/aptpod/iscp-go/transport"
	"github.com/aptpod/iscp-go/transport/metrics"
)

// TransportInfo はECFスケジューラで使用されるトランスポートの情報を保持します。
// 各トランスポートのメトリクスとステータスを管理し、ECFアルゴリズムの不等式評価に必要な情報を提供します。
type TransportInfo struct {
	transportID     transport.TransportID
	metricsProvider metrics.MetricsProvider
	sendingAllowed  bool

	// minRTT は観測された最小RTT（ベースRTT）を保持します。
	// キューイング遅延を除いたネットワーク遅延を推定するために使用されます。
	minRTT time.Duration
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
// sendingAllowed フラグと minRTT を更新します。
//
// sendingAllowed は以下の条件で true になります:
//
//	BytesInFlight < CongestionWindow
//
// これにより、送信可能な帯域幅があるかどうかを判定します。
//
// minRTT は観測された最小のRTTを追跡します。
// これにより、キューイング遅延を除いたベースRTTを推定できます。
func (p *TransportInfo) Update() {
	if p.metricsProvider == nil {
		p.sendingAllowed = false
		return
	}

	bytesInFlight := p.metricsProvider.BytesInFlight()
	cwnd := p.metricsProvider.CongestionWindow()

	p.sendingAllowed = bytesInFlight < cwnd

	// 最小RTTを追跡
	currentRTT := p.metricsProvider.RTT()
	if currentRTT > 0 && (p.minRTT == 0 || currentRTT < p.minRTT) {
		p.minRTT = currentRTT
	}
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

// MinRTT はこのトランスポートで観測された最小RTT（ベースRTT）を返します。
// キューイング遅延を除いたネットワーク遅延を推定するために使用されます。
// まだ観測されていない場合は SmoothedRTT() と同じ値を返します。
func (p *TransportInfo) MinRTT() time.Duration {
	if p.minRTT == 0 {
		return p.SmoothedRTT()
	}
	return p.minRTT
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
