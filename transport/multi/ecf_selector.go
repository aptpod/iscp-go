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

// ECFState は ECF アルゴリズムの状態を保持します。
// SelectTransportECF 関数で使用されます。
type ECFState struct {
	Waiting             bool
	WaitingForTransport transport.SubConnectionID
	QueueSize           uint64
	LastSelected        transport.SubConnectionID
	WaitPollInterval    time.Duration
	Logger              log.Logger

	// 統計カウンタ
	TotalSelections           atomic.Uint64
	FirstInequalityTrueCount  atomic.Uint64
	SecondInequalityTrueCount atomic.Uint64
	ActualWaitCount           atomic.Uint64
	SwitchCount               atomic.Uint64
	SelectionCountsMu         sync.Mutex
	SelectionCounts           map[transport.SubConnectionID]uint64

	// 内部バッファ（アロケーション回避用、SelectTransportECF 内部で管理）
	metricsBuffer []ecfTransportMetricEntry
}

// NewECFState は新しい ECFState を作成します。
func NewECFState() *ECFState {
	return &ECFState{
		WaitPollInterval: defaultWaitPollInterval,
		Logger:           log.NewNop(),
		SelectionCounts:  make(map[transport.SubConnectionID]uint64),
	}
}

// ECFSelector は ECF (Earliest Completion First) アルゴリズムを実装した TransportSelector です。
// 各トランスポートのRTT、輻輳ウィンドウ、送信中バイト数などのメトリクスを考慮して、
// 最も早く完了すると予測されるトランスポートを動的に選択します。
type ECFSelector struct {
	transportsMu   sync.RWMutex
	multiTransport *Transport
	transports     map[transport.SubConnectionID]*TransportInfo
	// quotas は将来の拡張のために保持
	quotas map[transport.SubConnectionID]uint

	stateMu  sync.Mutex
	ecfState *ECFState
}

type ecfTransportMetricEntry struct {
	id      transport.SubConnectionID
	metrics ecfTransportMetrics
	minRTT  uint64
}

// ECFStats は ECFSelector の統計情報を保持します。
type ECFStats struct {
	SelectionCounts           map[transport.SubConnectionID]uint64
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
		transports: make(map[transport.SubConnectionID]*TransportInfo),
		quotas:     make(map[transport.SubConnectionID]uint),
		ecfState:   NewECFState(),
	}
}

// SetLogger はロガーを設定します。TransportMetricsUpdater を実装します。
func (s *ECFSelector) SetLogger(logger log.Logger) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.ecfState.Logger = logger
}

// SetMultiTransport はマルチトランスポートへの参照を設定します。
func (s *ECFSelector) SetMultiTransport(mt *Transport) {
	s.transportsMu.Lock()
	defer s.transportsMu.Unlock()
	s.multiTransport = mt
}

// UpdateTransport はトランスポートのメトリクス情報を更新します。
func (s *ECFSelector) UpdateTransport(transportID transport.SubConnectionID, info *TransportInfo) {
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
	s.ecfState.QueueSize = queueSize
}

// SetWaitPollInterval は待機状態時のポーリング間隔を設定します。
func (s *ECFSelector) SetWaitPollInterval(interval time.Duration) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.ecfState.WaitPollInterval = interval
}

// Get は TransportSelector を実装し、ECFアルゴリズムで次に使用すべきトランスポートを返します。
// 利用可能なものがない場合は空文字列を返します。
// ctx がキャンセルされた場合も空文字列を返します。
func (s *ECFSelector) Get(ctx context.Context, _ int64) transport.SubConnectionID {
	var selectedID transport.SubConnectionID
	firstEvaluation := true
	for {
		selected := s.selectTransportECF(firstEvaluation)
		firstEvaluation = false
		if selected != "" {
			selectedID = selected
			break
		}
		s.stateMu.Lock()
		shouldWait := s.ecfState.Waiting
		pollInterval := s.ecfState.WaitPollInterval
		s.stateMu.Unlock()
		if !shouldWait {
			return ""
		}
		select {
		case <-time.After(pollInterval):
		case <-ctx.Done():
			return ""
		}
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
func (s *ECFSelector) selectTransportECF(recordStats bool) transport.SubConnectionID {
	// ロック順序: stateMu -> transportsMu（デッドロック防止）
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	s.transportsMu.RLock()
	defer s.transportsMu.RUnlock()

	return SelectTransportECF(s.transports, s.ecfState, recordStats)
}

// SelectTransportECF は ECF アルゴリズムでトランスポートを選択するスタンドアロン関数です。
// transports マップと state の同期は呼び出し元の責任です。
// firstEvaluation が true の場合、統計カウンタを記録します。
func SelectTransportECF(
	transports map[transport.SubConnectionID]*TransportInfo,
	state *ECFState,
	firstEvaluation bool,
) transport.SubConnectionID {
	transportCount := len(transports)

	if transportCount == 0 {
		return ""
	}

	if transportCount == 1 {
		var id transport.SubConnectionID
		for tid := range transports {
			id = tid
		}

		state.Waiting = false
		state.SelectionCountsMu.Lock()
		state.SelectionCounts[id]++
		state.SelectionCountsMu.Unlock()
		state.TotalSelections.Add(1)
		state.LastSelected = id
		return id
	}

	// MinRTT: キューイング遅延を除いた本来のネットワーク遅延で絶対最速トランスポートを判定
	// SmoothedRTT: 現在のRTTでECF不等式評価と送信可能最速トランスポートを判定
	var minRTTTransport transport.SubConnectionID
	var availableMinRTTTransport transport.SubConnectionID
	minBaseRTT := ^uint64(0)
	availableMinRTT := ^uint64(0)

	state.metricsBuffer = state.metricsBuffer[:0]

	for id, info := range transports {
		rtt := rttToMicroseconds(info.SmoothedRTT())
		baseRTT := rttToMicroseconds(info.MinRTT())
		m := ecfTransportMetrics{
			rtt:            rtt,
			rttvar:         rttToMicroseconds(info.MeanDeviation()),
			cwnd:           info.CongestionWindow(),
			bytesInFlight:  info.BytesInFlight(),
			sendingAllowed: info.SendingAllowed(),
		}
		state.metricsBuffer = append(state.metricsBuffer, ecfTransportMetricEntry{id: id, metrics: m, minRTT: baseRTT})

		if baseRTT < minBaseRTT {
			minBaseRTT = baseRTT
			minRTTTransport = id
		}

		if m.sendingAllowed && rtt < availableMinRTT {
			availableMinRTT = rtt
			availableMinRTTTransport = id
		}
	}

	if availableMinRTTTransport == "" {
		return ""
	}

	if minRTTTransport == availableMinRTTTransport {
		state.Waiting = false
		selected := minRTTTransport
		if state.LastSelected != "" && state.LastSelected != selected {
			state.SwitchCount.Add(1)
			logECFSwitchFromBuffer(state, selected, "fastest and available")
		}
		state.SelectionCountsMu.Lock()
		state.SelectionCounts[selected]++
		state.SelectionCountsMu.Unlock()
		state.TotalSelections.Add(1)
		state.LastSelected = selected
		return selected
	}

	// 第1不等式: β * lhs < β*rhs + waiting*rhs
	// lhs = srtt_f * (x_f + cwnd_f), rhs = cwnd_f * (srtt_s + delta)
	minRTTMetrics := getMetricsFromECFBuffer(state.metricsBuffer, minRTTTransport)
	availableMetrics := getMetricsFromECFBuffer(state.metricsBuffer, availableMinRTTTransport)

	srtt_f := minRTTMetrics.rtt
	srtt_s := availableMetrics.rtt
	rttvar_f := minRTTMetrics.rttvar
	rttvar_s := availableMetrics.rttvar

	cwnd_f := minRTTMetrics.cwnd
	cwnd_s := availableMetrics.cwnd

	delta := max(rttvar_f, rttvar_s)
	x_f := max(state.QueueSize, cwnd_f)
	lhs := srtt_f * (x_f + cwnd_f)
	rhs := cwnd_f * (srtt_s + delta)

	betaLhs := ecfBeta * lhs
	betaRhs := ecfBeta * rhs
	var waitingRhs uint64
	if state.Waiting {
		waitingRhs = rhs
	}

	firstInequalityTrue := betaLhs < (betaRhs + waitingRhs)

	if !firstInequalityTrue {
		state.Waiting = false
		selected := availableMinRTTTransport
		if state.LastSelected != "" && state.LastSelected != selected {
			state.SwitchCount.Add(1)
			logECFSwitchWithInequalityFromBuffer(state, selected, "1st inequality false",
				minRTTTransport, availableMinRTTTransport,
				srtt_f, srtt_s, rttvar_f, rttvar_s, cwnd_f, cwnd_s, delta,
				betaLhs, betaRhs, waitingRhs, firstInequalityTrue, 0, 0, false)
		}
		state.SelectionCountsMu.Lock()
		state.SelectionCounts[selected]++
		state.SelectionCountsMu.Unlock()
		state.TotalSelections.Add(1)
		state.LastSelected = selected
		return selected
	}

	if firstEvaluation {
		state.FirstInequalityTrueCount.Add(1)
	}

	// 第2不等式: lhs_s >= rhs_s
	// lhs_s = srtt_s * x_s, rhs_s = cwnd_s * (2*srtt_f + delta)
	x_s := max(state.QueueSize, cwnd_s)
	lhs_s := srtt_s * x_s
	rhs_s := cwnd_s * (2*srtt_f + delta)

	secondInequalityTrue := lhs_s >= rhs_s

	if firstEvaluation && secondInequalityTrue {
		state.SecondInequalityTrueCount.Add(1)
	}

	if secondInequalityTrue {
		state.Waiting = true
		state.WaitingForTransport = minRTTTransport
		if firstEvaluation {
			state.ActualWaitCount.Add(1)
		}
		return ""
	}

	state.Waiting = false
	selected := availableMinRTTTransport
	if state.LastSelected != "" && state.LastSelected != selected {
		state.SwitchCount.Add(1)
		logECFSwitchWithInequalityFromBuffer(state, selected, "2nd inequality false",
			minRTTTransport, availableMinRTTTransport,
			srtt_f, srtt_s, rttvar_f, rttvar_s, cwnd_f, cwnd_s, delta,
			betaLhs, betaRhs, waitingRhs, firstInequalityTrue, lhs_s, rhs_s, secondInequalityTrue)
	}
	state.SelectionCountsMu.Lock()
	state.SelectionCounts[selected]++
	state.SelectionCountsMu.Unlock()
	state.TotalSelections.Add(1)
	state.LastSelected = selected
	return selected
}

// Stats は統計情報のスナップショットを返します。
func (s *ECFSelector) Stats() ECFStats {
	s.ecfState.SelectionCountsMu.Lock()
	counts := make(map[transport.SubConnectionID]uint64, len(s.ecfState.SelectionCounts))
	maps.Copy(counts, s.ecfState.SelectionCounts)
	s.ecfState.SelectionCountsMu.Unlock()

	return ECFStats{
		SelectionCounts:           counts,
		TotalSelections:           s.ecfState.TotalSelections.Load(),
		FirstInequalityTrueCount:  s.ecfState.FirstInequalityTrueCount.Load(),
		SecondInequalityTrueCount: s.ecfState.SecondInequalityTrueCount.Load(),
		ActualWaitCount:           s.ecfState.ActualWaitCount.Load(),
		SwitchCount:               s.ecfState.SwitchCount.Load(),
	}
}

// ResetStats は統計情報をリセットします。
func (s *ECFSelector) ResetStats() {
	s.ecfState.SelectionCountsMu.Lock()
	s.ecfState.SelectionCounts = make(map[transport.SubConnectionID]uint64)
	s.ecfState.SelectionCountsMu.Unlock()

	s.ecfState.TotalSelections.Store(0)
	s.ecfState.FirstInequalityTrueCount.Store(0)
	s.ecfState.SecondInequalityTrueCount.Store(0)
	s.ecfState.ActualWaitCount.Store(0)
	s.ecfState.SwitchCount.Store(0)
}

// TransportMinRTT はトランスポートのMinRTTを返します。存在しない場合は0を返します。
func (s *ECFSelector) TransportMinRTT(transportID transport.SubConnectionID) time.Duration {
	s.transportsMu.RLock()
	defer s.transportsMu.RUnlock()

	if info, exists := s.transports[transportID]; exists {
		return info.MinRTT()
	}
	return 0
}

// getMetricsFromECFBuffer はバッファから指定されたトランスポートのメトリクスを取得します。
func getMetricsFromECFBuffer(buffer []ecfTransportMetricEntry, id transport.SubConnectionID) ecfTransportMetrics {
	for i := range buffer {
		if buffer[i].id == id {
			return buffer[i].metrics
		}
	}
	return ecfTransportMetrics{}
}

// logECFSwitchFromBuffer はトランスポート切り替えをログ出力します。
func logECFSwitchFromBuffer(state *ECFState, selected transport.SubConnectionID, reason string) {
	state.Logger.Infof(context.Background(), "ECF: SWITCH %s -> %s (%s)", state.LastSelected, selected, reason)
	for i := range state.metricsBuffer {
		entry := &state.metricsBuffer[i]
		minRTT := float64(entry.minRTT) / 1000.0
		m := entry.metrics
		state.Logger.Infof(context.Background(), "ECF:   [%s] RTT=%.2fms (MinRTT=%.2fms), CWND=%d, BytesInFlight=%d, SendingAllowed=%v", entry.id, float64(m.rtt)/1000.0, minRTT, m.cwnd, m.bytesInFlight, m.sendingAllowed)
	}
}

// logECFSwitchWithInequalityFromBuffer はトランスポート切り替えを不等式情報と共にログ出力します。
func logECFSwitchWithInequalityFromBuffer(state *ECFState, selected transport.SubConnectionID, reason string, minRTTTransport, availableMinRTTTransport transport.SubConnectionID, srtt_f, srtt_s, rttvar_f, rttvar_s, cwnd_f, cwnd_s, delta uint64, betaLhs, betaRhs, waitingRhs uint64, firstIneq bool, lhs_s, rhs_s uint64, secondIneq bool) {
	state.Logger.Infof(context.Background(),
		"ECF: SWITCH %s -> %s (%s)",
		state.LastSelected, selected, reason)

	for i := range state.metricsBuffer {
		entry := &state.metricsBuffer[i]
		minRTT := float64(entry.minRTT) / 1000.0
		m := entry.metrics
		state.Logger.Infof(context.Background(), "ECF:   [%s] RTT=%.2fms (MinRTT=%.2fms), RTTVar=%.2fms, CWND=%d, BytesInFlight=%d, SendingAllowed=%v", entry.id, float64(m.rtt)/1000.0, minRTT, float64(m.rttvar)/1000.0, m.cwnd, m.bytesInFlight, m.sendingAllowed)
	}

	state.Logger.Infof(context.Background(), "ECF:   fastest=%s, available=%s, delta=%.2fms, queueSize=%d", minRTTTransport, availableMinRTTTransport, float64(delta)/1000.0, state.QueueSize)
	state.Logger.Infof(context.Background(), "ECF:   1st ineq: βLhs=%d %s βRhs+wait=%d => %v", betaLhs, cmpSign(firstIneq), betaRhs+waitingRhs, firstIneq)
	if firstIneq {
		state.Logger.Infof(context.Background(), "ECF:   2nd ineq: lhs_s=%d %s rhs_s=%d => %v", lhs_s, cmpSignGe(secondIneq), rhs_s, secondIneq)
	}
}

type ecfTransportMetrics struct {
	rtt            uint64
	rttvar         uint64
	cwnd           uint64
	bytesInFlight  uint64
	sendingAllowed bool
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
