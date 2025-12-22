package multi

import (
	"context"
	"sync"
	"time"

	"github.com/aptpod/iscp-go/log"
	"github.com/aptpod/iscp-go/transport"
)

// ECFSelector は ECF (Earliest Completion First) アルゴリズムを実装した TransportSelector です。
// 複数のトランスポートから、最も早く完了すると予測されるトランスポートを選択します。
//
// ECFアルゴリズムは、各トランスポートのRTT、輻輳ウィンドウ、送信中バイト数などの
// メトリクスを考慮して、最適なトランスポートを動的に選択します。
//
// アルゴリズムの詳細:
//   - 第1不等式: 最速トランスポートを待つべきか評価
//   - 第2不等式: 待機が本当に有益か判定
//
// スレッドセーフ: 全てのメソッドは sync.RWMutex で保護されています。
type ECFSelector struct {
	// multiTransport は管理対象のマルチトランスポートへの参照です。
	multiTransport *Transport

	// transports は各トランスポートのメトリクス情報を保持します。
	// キー: TransportID, 値: TransportInfo
	transports map[transport.TransportID]*TransportInfo

	// quotas は各トランスポートのクォータ（割り当て量）を管理します。
	// 現在の実装では使用されていませんが、将来の拡張のために保持されています。
	quotas map[transport.TransportID]uint

	// waiting は現在待機中かどうかを示すフラグです。
	// true の場合、最速トランスポートが利用可能になるまで待機します。
	waiting bool

	// waitingForTransport は待機中のトランスポートIDを保持します。
	// waiting == true の場合のみ有効です。
	waitingForTransport transport.TransportID

	// queueSize は送信待ちキューのバイト数です。
	// ECF不等式の x_f, x_s 計算に使用されます。
	queueSize uint64

	// lastSelectedTransport は前回選択されたトランスポートIDです。
	// スイッチ検出のために使用されます。
	lastSelectedTransport transport.TransportID

	// mu は全てのフィールドへの並行アクセスを保護します。
	mu sync.RWMutex

	// logger はログ出力用のロガーです。
	logger log.Logger

	// === 統計情報フィールド ===

	// selectionCounts は各トランスポートの選択回数を保持します。
	selectionCounts map[transport.TransportID]uint64

	// totalSelections は総選択回数です。
	totalSelections uint64

	// firstInequalityTrueCount は第1不等式がtrueになった回数です。
	// （最速トランスポートを待つべきと評価された回数）
	firstInequalityTrueCount uint64

	// secondInequalityTrueCount は第2不等式がtrueになった回数です。
	// （待機が有益と判定された回数）
	secondInequalityTrueCount uint64

	// actualWaitCount は実際に待機した回数です。
	// （第2不等式がtrueで待機が有益と判定された場合にカウント）
	actualWaitCount uint64

	// switchCount はトランスポートスイッチの回数です。
	switchCount uint64

	// waitPollInterval は待機状態時のポーリング間隔です。
	// Get() で待機判定された場合、この間隔でループしてトランスポートを再選択します。
	// デフォルトは 100 マイクロ秒です。
	waitPollInterval time.Duration
}

// ECFStats は ECFSelector の統計情報を保持します。
type ECFStats struct {
	// SelectionCounts は各トランスポートの選択回数です。
	SelectionCounts map[transport.TransportID]uint64

	// TotalSelections は総選択回数です。
	TotalSelections uint64

	// FirstInequalityTrueCount は第1不等式がtrueになった回数です。
	FirstInequalityTrueCount uint64

	// SecondInequalityTrueCount は第2不等式がtrueになった回数です。
	SecondInequalityTrueCount uint64

	// ActualWaitCount は実際に待機した回数です。
	ActualWaitCount uint64

	// SwitchCount はトランスポートスイッチの回数です。
	SwitchCount uint64
}

// NewECFSelector は新しい ECFSelector を作成します。
//
// 使用例:
//
//	// ECFSelector を作成
//	selector := multi.NewECFSelector()
//
//	// multi.Transport を作成
//	// ECFSelector を使用する場合、メトリクス更新ループが自動的に起動されます
//	mt, err := multi.NewTransport(multi.TransportConfig{
//	    TransportMap:      transportMap,
//	    TransportSelector: selector,
//	    Logger:            logger,
//	})
//	if err != nil {
//	    return err
//	}
//	defer mt.Close()
//
//	// データの書き込み
//	// ECFアルゴリズムにより、最適なトランスポートが自動的に選択されます
//	err = mt.Write(data)
//
// defaultWaitPollInterval はデフォルトの待機ポーリング間隔です。
const defaultWaitPollInterval = 100 * time.Microsecond

func NewECFSelector() *ECFSelector {
	return &ECFSelector{
		transports:       make(map[transport.TransportID]*TransportInfo),
		quotas:           make(map[transport.TransportID]uint),
		selectionCounts:  make(map[transport.TransportID]uint64),
		logger:           log.NewNop(),
		waitPollInterval: defaultWaitPollInterval,
	}
}

// SetLogger は ECFSelector にロガーを設定します。
// ECFTransportUpdater インターフェースを実装します。
func (s *ECFSelector) SetLogger(logger log.Logger) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logger = logger
}

// SetMultiTransport は管理対象のマルチトランスポートへの参照を設定します。
//
// この参照は、トランスポートのメトリクス取得やトランスポート一覧の取得に使用されます。
func (s *ECFSelector) SetMultiTransport(mt *Transport) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.multiTransport = mt
}

// UpdateTransport は指定されたトランスポートのメトリクス情報を更新します。
//
// transportID: 更新対象のトランスポートID
// info: 更新するメトリクス情報
//
// このメソッドは、MetricsProvider からメトリクスを取得した後に呼び出されます。
func (s *ECFSelector) UpdateTransport(transportID transport.TransportID, info *TransportInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// TransportInfo の Update() メソッドを呼び出して sendingAllowed フラグを更新
	info.Update()

	// transports マップに保存
	s.transports[transportID] = info
}

// SetQueueSize は送信待ちキューのサイズを設定します。
//
// queueSize: 送信待ちデータのバイト数
//
// このメソッドは、非同期送信の前後で呼び出され、ECF不等式の計算に使用されます。
func (s *ECFSelector) SetQueueSize(queueSize uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queueSize = queueSize
}

// SetWaitPollInterval は待機状態時のポーリング間隔を設定します。
//
// interval: ポーリング間隔
//
// Get() で待機判定された場合、この間隔でトランスポートの再選択を試みます。
// デフォルトは 100 マイクロ秒です。
func (s *ECFSelector) SetWaitPollInterval(interval time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.waitPollInterval = interval
}

// Get は TransportSelector インターフェースを実装し、
// 指定されたデータサイズに基づいて次に使用すべきトランスポートIDを返します。
//
// bsSize: 送信するデータのバイト数
//
// ECFアルゴリズムを使用してトランスポートを選択します。
// 待機が必要と判定された場合、waitPollInterval の間隔でポーリングし、
// トランスポートが選択されるまでループします。
//
// 選択されたトランスポートが利用不可（接続が確立していない等）の場合、
// 他の利用可能なトランスポートにフォールバックします。
//
// 戻り値:
//   - 選択されたトランスポートID（利用可能なものがない場合は空文字列）
func (s *ECFSelector) Get(bsSize int64) transport.TransportID {
	var selectedID transport.TransportID
	for {
		selected := s.selectTransportECF()
		if selected != "" {
			selectedID = selected
			break
		}
		// selectTransportECF が空文字列を返した場合:
		// - waiting=true: 待機が有益と判定されたので、ポーリングして再試行
		// - waiting=false: トランスポートがないか全て送信不可なので、そのまま返す
		s.mu.RLock()
		shouldWait := s.waiting
		s.mu.RUnlock()
		if !shouldWait {
			return ""
		}
		// 待機状態: ポーリング間隔だけ待機してから再試行
		time.Sleep(s.waitPollInterval)
	}

	// multiTransportが設定されていない場合はそのまま返す
	s.mu.RLock()
	mt := s.multiTransport
	s.mu.RUnlock()
	if mt == nil {
		return selectedID
	}

	// 選択されたトランスポートが利用可能か確認し、必要に応じてフォールバック
	return SelectAvailableTransport(selectedID, mt.Transports())
}

// selectTransportECF は ECF アルゴリズムを使用してトランスポートを選択します。
//
// このメソッドは、2つの不等式を評価して最適なトランスポートを選択します:
//   - 第1不等式: 最速トランスポートを待つべきか評価
//   - 第2不等式: 待機が本当に有益か判定
//
// 戻り値:
//   - 選択されたトランスポートID（待機の場合は空文字列）
//
// アルゴリズムの流れ:
//  1. トランスポート数が1以下の場合、特殊ケース処理
//  2. 絶対最速トランスポート (minRTTTransport) の探索
//  3. 送信可能最速トランスポート (availableMinRTTTransport) の探索
//  4. 両者が同一なら即座に返す
//  5. 第1不等式の評価
//  6. 第2不等式の評価 (第1が真の場合)
//  7. 待機判定とトランスポート選択
func (s *ECFSelector) selectTransportECF() transport.TransportID {
	s.mu.Lock()
	defer s.mu.Unlock()

	// トランスポートが存在しない場合
	if len(s.transports) == 0 {
		return ""
	}

	// トランスポートが1つのみの場合
	if len(s.transports) == 1 {
		for id := range s.transports {
			s.waiting = false
			s.recordSelection(id)
			s.lastSelectedTransport = id
			return id
		}
		// 到達しないコード（len == 1 の場合必ず return される）
		return ""
	}

	// 1. 絶対最速トランスポート (minRTTTransport) の探索
	var minRTTTransport transport.TransportID
	minRTT := ^uint64(0) // 最大値で初期化

	// 各トランスポートのメトリクスを収集
	metrics := make(map[transport.TransportID]ecfTransportMetrics)

	for id, info := range s.transports {
		rtt := rttToMicroseconds(info.SmoothedRTT())
		m := ecfTransportMetrics{
			rtt:            rtt,
			rttvar:         rttToMicroseconds(info.MeanDeviation()),
			cwnd:           info.CongestionWindow(),
			bytesInFlight:  info.BytesInFlight(),
			sendingAllowed: info.SendingAllowed(),
		}
		metrics[id] = m
		if rtt < minRTT {
			minRTT = rtt
			minRTTTransport = id
		}
	}

	// 2. 送信可能最速トランスポート (availableMinRTTTransport) の探索
	var availableMinRTTTransport transport.TransportID
	availableMinRTT := ^uint64(0)

	for id, m := range metrics {
		if !m.sendingAllowed {
			continue
		}
		if m.rtt < availableMinRTT {
			availableMinRTT = m.rtt
			availableMinRTTTransport = id
		}
	}

	// 送信可能なトランスポートがない場合
	if availableMinRTTTransport == "" {
		return ""
	}

	// 3. 両者が同一なら即座に返す
	if minRTTTransport == availableMinRTTTransport {
		s.waiting = false
		selected := minRTTTransport
		// スイッチ検出と統計更新
		if s.lastSelectedTransport != "" && s.lastSelectedTransport != selected {
			s.switchCount++
			s.logSwitch(selected, "fastest and available", metrics)
		}
		s.recordSelection(selected)
		s.lastSelectedTransport = selected
		return selected
	}

	// 4. 第1不等式の評価
	// β * lhs < β*rhs + waiting*rhs
	// lhs = srtt_f * (x_f + cwnd_f*mss)
	// rhs = cwnd_f * mss * (srtt_s + delta)

	minRTTMetrics := metrics[minRTTTransport]
	availableMetrics := metrics[availableMinRTTTransport]

	srtt_f := minRTTMetrics.rtt
	srtt_s := availableMetrics.rtt
	rttvar_f := minRTTMetrics.rttvar
	rttvar_s := availableMetrics.rttvar

	cwnd_f := minRTTMetrics.cwnd
	cwnd_s := availableMetrics.cwnd

	// delta = max(rttvar_f, rttvar_s)
	delta := max(rttvar_f, rttvar_s)

	// x_f = max(queueSize, cwnd_f)
	x_f := max(s.queueSize, cwnd_f)

	// 第1不等式の左辺と右辺を計算
	lhs := srtt_f * (x_f + cwnd_f)
	rhs := cwnd_f * (srtt_s + delta)

	// β * lhs < β*rhs + waiting*rhs
	betaLhs := ecfBeta * lhs
	betaRhs := ecfBeta * rhs
	var waitingRhs uint64
	if s.waiting {
		waitingRhs = rhs
	}

	firstInequalityTrue := betaLhs < (betaRhs + waitingRhs)

	// 第1不等式が偽の場合、即座に availableMinRTTTransport を返す
	if !firstInequalityTrue {
		s.waiting = false
		selected := availableMinRTTTransport
		// スイッチ検出と統計更新
		if s.lastSelectedTransport != "" && s.lastSelectedTransport != selected {
			s.switchCount++
			s.logSwitchWithInequality(selected, "1st inequality false", metrics,
				minRTTTransport, availableMinRTTTransport,
				srtt_f, srtt_s, rttvar_f, rttvar_s, cwnd_f, cwnd_s, delta,
				betaLhs, betaRhs, waitingRhs, firstInequalityTrue, 0, 0, false)
		}
		s.recordSelection(selected)
		s.lastSelectedTransport = selected
		return selected
	}

	// 第1不等式が真の場合の統計更新
	s.firstInequalityTrueCount++

	// 5. 第2不等式の評価 (第1が真の場合)
	// lhs_s >= rhs_s
	// lhs_s = srtt_s * x_s
	// rhs_s = cwnd_s * (2*srtt_f + delta)

	// x_s = max(queueSize, cwnd_s)
	x_s := max(s.queueSize, cwnd_s)

	lhs_s := srtt_s * x_s
	rhs_s := cwnd_s * (2*srtt_f + delta)

	secondInequalityTrue := lhs_s >= rhs_s

	// 第2不等式が真の場合の統計更新
	if secondInequalityTrue {
		s.secondInequalityTrueCount++
	}

	// 6. 待機判定とトランスポート選択
	if secondInequalityTrue {
		// 待機が有益: 最速トランスポートが利用可能になるまで待機
		s.waiting = true
		s.waitingForTransport = minRTTTransport
		s.actualWaitCount++
		return ""
	}

	// 待機しない: 送信可能最速トランスポートを使用
	s.waiting = false
	selected := availableMinRTTTransport
	// スイッチ検出と統計更新
	if s.lastSelectedTransport != "" && s.lastSelectedTransport != selected {
		s.switchCount++
		s.logSwitchWithInequality(selected, "2nd inequality false", metrics,
			minRTTTransport, availableMinRTTTransport,
			srtt_f, srtt_s, rttvar_f, rttvar_s, cwnd_f, cwnd_s, delta,
			betaLhs, betaRhs, waitingRhs, firstInequalityTrue, lhs_s, rhs_s, secondInequalityTrue)
	}
	s.recordSelection(selected)
	s.lastSelectedTransport = selected
	return selected
}

// recordSelection は選択統計を記録します（ロックは呼び出し元で取得済みと仮定）。
func (s *ECFSelector) recordSelection(id transport.TransportID) {
	s.selectionCounts[id]++
	s.totalSelections++
}

// Stats は ECFSelector の統計情報のスナップショットを返します。
//
// 返される ECFStats は呼び出し時点の統計のコピーであり、
// その後の ECFSelector の状態変更の影響を受けません。
func (s *ECFSelector) Stats() ECFStats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// selectionCounts のコピーを作成
	counts := make(map[transport.TransportID]uint64, len(s.selectionCounts))
	for id, count := range s.selectionCounts {
		counts[id] = count
	}

	return ECFStats{
		SelectionCounts:           counts,
		TotalSelections:           s.totalSelections,
		FirstInequalityTrueCount:  s.firstInequalityTrueCount,
		SecondInequalityTrueCount: s.secondInequalityTrueCount,
		ActualWaitCount:           s.actualWaitCount,
		SwitchCount:               s.switchCount,
	}
}

// ResetStats は統計情報をリセットします。
func (s *ECFSelector) ResetStats() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.selectionCounts = make(map[transport.TransportID]uint64)
	s.totalSelections = 0
	s.firstInequalityTrueCount = 0
	s.secondInequalityTrueCount = 0
	s.actualWaitCount = 0
	s.switchCount = 0
}

// logSwitch はトランスポートスイッチ時の簡易ログを出力します。
func (s *ECFSelector) logSwitch(selected transport.TransportID, reason string, metrics map[transport.TransportID]ecfTransportMetrics) {
	s.logger.Infof(context.Background(),
		"ECF: SWITCH %s -> %s (%s)",
		s.lastSelectedTransport, selected, reason)
	for id, m := range metrics {
		s.logger.Infof(context.Background(),
			"ECF:   [%s] RTT=%.2fms, CWND=%d, BytesInFlight=%d, SendingAllowed=%v",
			id, float64(m.rtt)/1000.0, m.cwnd, m.bytesInFlight, m.sendingAllowed)
	}
}

// logSwitchWithInequality はトランスポートスイッチ時の詳細ログを出力します。
func (s *ECFSelector) logSwitchWithInequality(
	selected transport.TransportID,
	reason string,
	metrics map[transport.TransportID]ecfTransportMetrics,
	minRTTTransport, availableMinRTTTransport transport.TransportID,
	srtt_f, srtt_s, rttvar_f, rttvar_s, cwnd_f, cwnd_s, delta uint64,
	betaLhs, betaRhs, waitingRhs uint64, firstIneq bool,
	lhs_s, rhs_s uint64, secondIneq bool,
) {
	s.logger.Infof(context.Background(),
		"ECF: SWITCH %s -> %s (%s)",
		s.lastSelectedTransport, selected, reason)

	// 各トランスポートのメトリクス
	for id, m := range metrics {
		s.logger.Infof(context.Background(),
			"ECF:   [%s] RTT=%.2fms, RTTVar=%.2fms, CWND=%d, BytesInFlight=%d, SendingAllowed=%v",
			id, float64(m.rtt)/1000.0, float64(m.rttvar)/1000.0, m.cwnd, m.bytesInFlight, m.sendingAllowed)
	}

	// 不等式評価のパラメータ
	s.logger.Infof(context.Background(),
		"ECF:   fastest=%s, available=%s, delta=%.2fms, queueSize=%d",
		minRTTTransport, availableMinRTTTransport, float64(delta)/1000.0, s.queueSize)

	// 第1不等式
	s.logger.Infof(context.Background(),
		"ECF:   1st ineq: βLhs=%d %s βRhs+wait=%d => %v",
		betaLhs, cmpSign(firstIneq), betaRhs+waitingRhs, firstIneq)

	// 第2不等式（評価された場合のみ）
	if firstIneq {
		s.logger.Infof(context.Background(),
			"ECF:   2nd ineq: lhs_s=%d %s rhs_s=%d => %v",
			lhs_s, cmpSignGe(secondIneq), rhs_s, secondIneq)
	}
}

// ecfTransportMetrics はトランスポートのメトリクス情報を保持します（ECF選択用）。
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
