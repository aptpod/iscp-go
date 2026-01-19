package multi

import (
	"context"
	"maps"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aptpod/iscp-go/log"
	"github.com/aptpod/iscp-go/transport"
)

// ECFSelector は ECF (Earliest Completion First) アルゴリズムを実装した TransportSelector です。
// 各トランスポートのRTT、輻輳ウィンドウ、送信中バイト数などのメトリクスを考慮して、
// 最も早く完了すると予測されるトランスポートを動的に選択します。
type ECFSelector struct {
	transportsMu   sync.RWMutex
	multiTransport *Transport
	transports     map[transport.TransportID]*TransportInfo
	// quotas は将来の拡張のために保持
	quotas map[transport.TransportID]uint

	stateMu             sync.Mutex
	waiting             bool
	waitingForTransport transport.TransportID
	// queueSize はECF不等式の x_f, x_s 計算に使用
	queueSize             uint64
	lastSelectedTransport transport.TransportID
	// metricsBuffer はマップアロケーションを避けるための事前確保バッファ
	metricsBuffer    []ecfTransportMetricEntry
	waitPollInterval time.Duration
	logger           log.Logger

	// 統計情報（atomic操作で保護、ロック不要）
	totalSelections           atomic.Uint64
	firstInequalityTrueCount  atomic.Uint64
	secondInequalityTrueCount atomic.Uint64
	actualWaitCount           atomic.Uint64
	switchCount               atomic.Uint64

	selectionCountsMu sync.Mutex
	selectionCounts   map[transport.TransportID]uint64
}

type ecfTransportMetricEntry struct {
	id      transport.TransportID
	metrics ecfTransportMetrics
	minRTT  uint64
}

// ECFStats は ECFSelector の統計情報を保持します。
type ECFStats struct {
	SelectionCounts           map[transport.TransportID]uint64
	TotalSelections           uint64
	FirstInequalityTrueCount  uint64
	SecondInequalityTrueCount uint64
	ActualWaitCount           uint64
	SwitchCount               uint64
}

const defaultWaitPollInterval = 100 * time.Microsecond

// NewECFSelector は新しい ECFSelector を作成します。
func NewECFSelector() *ECFSelector {
	return &ECFSelector{
		transports:       make(map[transport.TransportID]*TransportInfo),
		quotas:           make(map[transport.TransportID]uint),
		selectionCounts:  make(map[transport.TransportID]uint64),
		logger:           log.NewNop(),
		waitPollInterval: defaultWaitPollInterval,
	}
}

// SetLogger はロガーを設定します。TransportMetricsUpdater を実装します。
func (s *ECFSelector) SetLogger(logger log.Logger) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.logger = logger
}

// SetMultiTransport はマルチトランスポートへの参照を設定します。
func (s *ECFSelector) SetMultiTransport(mt *Transport) {
	s.transportsMu.Lock()
	defer s.transportsMu.Unlock()
	s.multiTransport = mt
}

// UpdateTransport はトランスポートのメトリクス情報を更新します。
func (s *ECFSelector) UpdateTransport(transportID transport.TransportID, info *TransportInfo) {
	s.transportsMu.Lock()
	defer s.transportsMu.Unlock()

	if existingInfo, exists := s.transports[transportID]; exists && existingInfo.minRTT > 0 {
		info.minRTT = existingInfo.minRTT
	}
	info.Update()
	s.transports[transportID] = info
}

// SetQueueSize は送信待ちキューのサイズを設定します。
func (s *ECFSelector) SetQueueSize(queueSize uint64) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.queueSize = queueSize
}

// SetWaitPollInterval は待機状態時のポーリング間隔を設定します。
func (s *ECFSelector) SetWaitPollInterval(interval time.Duration) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.waitPollInterval = interval
}

// Get は TransportSelector を実装し、ECFアルゴリズムで次に使用すべきトランスポートを返します。
// 利用可能なものがない場合は空文字列を返します。
func (s *ECFSelector) Get(bsSize int64) transport.TransportID {
	var selectedID transport.TransportID
	firstEvaluation := true
	for {
		selected := s.selectTransportECF(firstEvaluation)
		firstEvaluation = false
		if selected != "" {
			selectedID = selected
			break
		}
		s.stateMu.Lock()
		shouldWait := s.waiting
		pollInterval := s.waitPollInterval
		s.stateMu.Unlock()
		if !shouldWait {
			return ""
		}
		time.Sleep(pollInterval)
	}

	s.transportsMu.RLock()
	mt := s.multiTransport
	s.transportsMu.RUnlock()
	if mt == nil {
		return selectedID
	}

	return SelectAvailableTransport(selectedID, mt.Transports())
}

// selectTransportECF はECFアルゴリズムでトランスポートを選択します。
// 待機が有益と判断された場合は空文字列を返します。
func (s *ECFSelector) selectTransportECF(recordStats bool) transport.TransportID {
	// ロック順序: stateMu -> transportsMu（デッドロック防止）
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	s.transportsMu.RLock()
	transportCount := len(s.transports)
	s.transportsMu.RUnlock()

	if transportCount == 0 {
		return ""
	}

	if transportCount == 1 {
		s.transportsMu.RLock()
		var id transport.TransportID
		for tid := range s.transports {
			id = tid
		}
		s.transportsMu.RUnlock()

		s.waiting = false
		s.recordSelection(id)
		s.lastSelectedTransport = id
		return id
	}

	// MinRTT: キューイング遅延を除いた本来のネットワーク遅延で絶対最速トランスポートを判定
	// SmoothedRTT: 現在のRTTでECF不等式評価と送信可能最速トランスポートを判定
	var minRTTTransport transport.TransportID
	var availableMinRTTTransport transport.TransportID
	minBaseRTT := ^uint64(0)
	availableMinRTT := ^uint64(0)

	s.metricsBuffer = s.metricsBuffer[:0]

	s.transportsMu.RLock()
	for id, info := range s.transports {
		rtt := rttToMicroseconds(info.SmoothedRTT())
		baseRTT := rttToMicroseconds(info.MinRTT())
		m := ecfTransportMetrics{
			rtt:            rtt,
			rttvar:         rttToMicroseconds(info.MeanDeviation()),
			cwnd:           info.CongestionWindow(),
			bytesInFlight:  info.BytesInFlight(),
			sendingAllowed: info.SendingAllowed(),
		}
		s.metricsBuffer = append(s.metricsBuffer, ecfTransportMetricEntry{id: id, metrics: m, minRTT: baseRTT})

		if baseRTT < minBaseRTT {
			minBaseRTT = baseRTT
			minRTTTransport = id
		}

		if m.sendingAllowed && rtt < availableMinRTT {
			availableMinRTT = rtt
			availableMinRTTTransport = id
		}
	}
	s.transportsMu.RUnlock()

	if availableMinRTTTransport == "" {
		return ""
	}

	if minRTTTransport == availableMinRTTTransport {
		s.waiting = false
		selected := minRTTTransport
		if s.lastSelectedTransport != "" && s.lastSelectedTransport != selected {
			s.switchCount.Add(1)
			s.logSwitchFromBuffer(selected, "fastest and available")
		}
		s.recordSelection(selected)
		s.lastSelectedTransport = selected
		return selected
	}

	// 第1不等式: β * lhs < β*rhs + waiting*rhs
	// lhs = srtt_f * (x_f + cwnd_f), rhs = cwnd_f * (srtt_s + delta)
	minRTTMetrics := s.getMetricsFromBuffer(minRTTTransport)
	availableMetrics := s.getMetricsFromBuffer(availableMinRTTTransport)

	srtt_f := minRTTMetrics.rtt
	srtt_s := availableMetrics.rtt
	rttvar_f := minRTTMetrics.rttvar
	rttvar_s := availableMetrics.rttvar

	cwnd_f := minRTTMetrics.cwnd
	cwnd_s := availableMetrics.cwnd

	delta := max(rttvar_f, rttvar_s)
	x_f := max(s.queueSize, cwnd_f)
	lhs := srtt_f * (x_f + cwnd_f)
	rhs := cwnd_f * (srtt_s + delta)

	betaLhs := ecfBeta * lhs
	betaRhs := ecfBeta * rhs
	var waitingRhs uint64
	if s.waiting {
		waitingRhs = rhs
	}

	firstInequalityTrue := betaLhs < (betaRhs + waitingRhs)

	if !firstInequalityTrue {
		s.waiting = false
		selected := availableMinRTTTransport
		if s.lastSelectedTransport != "" && s.lastSelectedTransport != selected {
			s.switchCount.Add(1)
			s.logSwitchWithInequalityFromBuffer(selected, "1st inequality false",
				minRTTTransport, availableMinRTTTransport,
				srtt_f, srtt_s, rttvar_f, rttvar_s, cwnd_f, cwnd_s, delta,
				betaLhs, betaRhs, waitingRhs, firstInequalityTrue, 0, 0, false)
		}
		s.recordSelection(selected)
		s.lastSelectedTransport = selected
		return selected
	}

	if recordStats {
		s.firstInequalityTrueCount.Add(1)
	}

	// 第2不等式: lhs_s >= rhs_s
	// lhs_s = srtt_s * x_s, rhs_s = cwnd_s * (2*srtt_f + delta)
	x_s := max(s.queueSize, cwnd_s)
	lhs_s := srtt_s * x_s
	rhs_s := cwnd_s * (2*srtt_f + delta)

	secondInequalityTrue := lhs_s >= rhs_s

	if recordStats && secondInequalityTrue {
		s.secondInequalityTrueCount.Add(1)
	}

	if secondInequalityTrue {
		s.waiting = true
		s.waitingForTransport = minRTTTransport
		if recordStats {
			s.actualWaitCount.Add(1)
		}
		return ""
	}

	s.waiting = false
	selected := availableMinRTTTransport
	if s.lastSelectedTransport != "" && s.lastSelectedTransport != selected {
		s.switchCount.Add(1)
		s.logSwitchWithInequalityFromBuffer(selected, "2nd inequality false",
			minRTTTransport, availableMinRTTTransport,
			srtt_f, srtt_s, rttvar_f, rttvar_s, cwnd_f, cwnd_s, delta,
			betaLhs, betaRhs, waitingRhs, firstInequalityTrue, lhs_s, rhs_s, secondInequalityTrue)
	}
	s.recordSelection(selected)
	s.lastSelectedTransport = selected
	return selected
}

func (s *ECFSelector) recordSelection(id transport.TransportID) {
	s.selectionCountsMu.Lock()
	s.selectionCounts[id]++
	s.selectionCountsMu.Unlock()
	s.totalSelections.Add(1)
}

// Stats は統計情報のスナップショットを返します。
func (s *ECFSelector) Stats() ECFStats {
	s.selectionCountsMu.Lock()
	counts := make(map[transport.TransportID]uint64, len(s.selectionCounts))
	maps.Copy(counts, s.selectionCounts)
	s.selectionCountsMu.Unlock()

	return ECFStats{
		SelectionCounts:           counts,
		TotalSelections:           s.totalSelections.Load(),
		FirstInequalityTrueCount:  s.firstInequalityTrueCount.Load(),
		SecondInequalityTrueCount: s.secondInequalityTrueCount.Load(),
		ActualWaitCount:           s.actualWaitCount.Load(),
		SwitchCount:               s.switchCount.Load(),
	}
}

// ResetStats は統計情報をリセットします。
func (s *ECFSelector) ResetStats() {
	s.selectionCountsMu.Lock()
	s.selectionCounts = make(map[transport.TransportID]uint64)
	s.selectionCountsMu.Unlock()

	s.totalSelections.Store(0)
	s.firstInequalityTrueCount.Store(0)
	s.secondInequalityTrueCount.Store(0)
	s.actualWaitCount.Store(0)
	s.switchCount.Store(0)
}

// TransportMinRTT はトランスポートのMinRTTを返します。存在しない場合は0を返します。
func (s *ECFSelector) TransportMinRTT(transportID transport.TransportID) time.Duration {
	s.transportsMu.RLock()
	defer s.transportsMu.RUnlock()

	if info, exists := s.transports[transportID]; exists {
		return info.MinRTT()
	}
	return 0
}

func (s *ECFSelector) logSwitchFromBuffer(selected transport.TransportID, reason string) {
	s.logger.Infof(context.Background(), "ECF: SWITCH %s -> %s (%s)", s.lastSelectedTransport, selected, reason)
	for i := range s.metricsBuffer {
		entry := &s.metricsBuffer[i]
		minRTT := float64(entry.minRTT) / 1000.0
		m := entry.metrics
		s.logger.Infof(context.Background(), "ECF:   [%s] RTT=%.2fms (MinRTT=%.2fms), CWND=%d, BytesInFlight=%d, SendingAllowed=%v", entry.id, float64(m.rtt)/1000.0, minRTT, m.cwnd, m.bytesInFlight, m.sendingAllowed)
	}
}

func (s *ECFSelector) logSwitchWithInequalityFromBuffer(selected transport.TransportID, reason string, minRTTTransport, availableMinRTTTransport transport.TransportID, srtt_f, srtt_s, rttvar_f, rttvar_s, cwnd_f, cwnd_s, delta uint64, betaLhs, betaRhs, waitingRhs uint64, firstIneq bool, lhs_s, rhs_s uint64, secondIneq bool) {
	s.logger.Infof(context.Background(),
		"ECF: SWITCH %s -> %s (%s)",
		s.lastSelectedTransport, selected, reason)

	for i := range s.metricsBuffer {
		entry := &s.metricsBuffer[i]
		minRTT := float64(entry.minRTT) / 1000.0
		m := entry.metrics
		s.logger.Infof(context.Background(), "ECF:   [%s] RTT=%.2fms (MinRTT=%.2fms), RTTVar=%.2fms, CWND=%d, BytesInFlight=%d, SendingAllowed=%v", entry.id, float64(m.rtt)/1000.0, minRTT, float64(m.rttvar)/1000.0, m.cwnd, m.bytesInFlight, m.sendingAllowed)
	}

	s.logger.Infof(context.Background(), "ECF:   fastest=%s, available=%s, delta=%.2fms, queueSize=%d", minRTTTransport, availableMinRTTTransport, float64(delta)/1000.0, s.queueSize)
	s.logger.Infof(context.Background(), "ECF:   1st ineq: βLhs=%d %s βRhs+wait=%d => %v", betaLhs, cmpSign(firstIneq), betaRhs+waitingRhs, firstIneq)
	if firstIneq {
		s.logger.Infof(context.Background(), "ECF:   2nd ineq: lhs_s=%d %s rhs_s=%d => %v", lhs_s, cmpSignGe(secondIneq), rhs_s, secondIneq)
	}
}

type ecfTransportMetrics struct {
	rtt            uint64
	rttvar         uint64
	cwnd           uint64
	bytesInFlight  uint64
	sendingAllowed bool
}

func (s *ECFSelector) getMetricsFromBuffer(id transport.TransportID) ecfTransportMetrics {
	for i := range s.metricsBuffer {
		if s.metricsBuffer[i].id == id {
			return s.metricsBuffer[i].metrics
		}
	}
	return ecfTransportMetrics{}
}

func cmpSign(less bool) string {
	if less {
		return "<"
	}
	return ">="
}

func cmpSignGe(ge bool) string {
	if ge {
		return ">="
	}
	return "<"
}
