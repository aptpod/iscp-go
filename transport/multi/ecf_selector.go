package multi

import (
	"sync"

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

	// waiting は現在待機中かどうかを示すフラグです（0 または 1）。
	// 1 の場合、最速トランスポートが利用可能になるまで待機します。
	waiting uint8

	// waitingForTransport は待機中のトランスポートIDを保持します。
	// waiting == 1 の場合のみ有効です。
	waitingForTransport transport.TransportID

	// queueSize は送信待ちキューのバイト数です。
	// ECF不等式の x_f, x_s 計算に使用されます。
	queueSize uint64

	// mu は全てのフィールドへの並行アクセスを保護します。
	mu sync.RWMutex
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
func NewECFSelector() *ECFSelector {
	return &ECFSelector{
		transports: make(map[transport.TransportID]*TransportInfo),
		quotas:     make(map[transport.TransportID]uint),
		waiting:    0,
	}
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

// Get は TransportSelector インターフェースを実装し、
// 指定されたデータサイズに基づいて次に使用すべきトランスポートIDを返します。
//
// bsSize: 送信するデータのバイト数
//
// ECFアルゴリズムを使用してトランスポートを選択します。
// 待機が必要な場合は空文字列を返します。
//
// 戻り値:
//   - 選択されたトランスポートID（待機の場合は空文字列）
func (s *ECFSelector) Get(bsSize int64) transport.TransportID {
	return s.SelectTransportEarliestCompletionFirst(false)
}

// SelectTransportEarliestCompletionFirst は ECF アルゴリズムを使用してトランスポートを選択します。
//
// このメソッドは、2つの不等式を評価して最適なトランスポートを選択します:
//   - 第1不等式: 最速トランスポートを待つべきか評価
//   - 第2不等式: 待機が本当に有益か判定
//
// allowWaiting: true の場合、待機判定を許可します（空文字列を返す可能性あり）
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
func (s *ECFSelector) SelectTransportEarliestCompletionFirst(allowWaiting bool) transport.TransportID {
	s.mu.Lock()
	defer s.mu.Unlock()

	// トランスポートが存在しない場合
	if len(s.transports) == 0 {
		return ""
	}

	// トランスポートが1つのみの場合
	if len(s.transports) == 1 {
		for id := range s.transports {
			s.waiting = 0
			return id
		}
		// 到達しないコード（len == 1 の場合必ず return される）
		return ""
	}

	// 1. 絶対最速トランスポート (minRTTTransport) の探索
	var minRTTTransport transport.TransportID
	minRTT := ^uint64(0) // 最大値で初期化

	for id, info := range s.transports {
		rtt := rttToMicroseconds(info.SmoothedRTT())
		if rtt < minRTT {
			minRTT = rtt
			minRTTTransport = id
		}
	}

	// 2. 送信可能最速トランスポート (availableMinRTTTransport) の探索
	var availableMinRTTTransport transport.TransportID
	availableMinRTT := ^uint64(0)

	for id, info := range s.transports {
		if !info.SendingAllowed() {
			continue
		}
		rtt := rttToMicroseconds(info.SmoothedRTT())
		if rtt < availableMinRTT {
			availableMinRTT = rtt
			availableMinRTTTransport = id
		}
	}

	// 送信可能なトランスポートがない場合
	if availableMinRTTTransport == "" {
		return ""
	}

	// 3. 両者が同一なら即座に返す
	if minRTTTransport == availableMinRTTTransport {
		s.waiting = 0
		return minRTTTransport
	}

	// 4. 第1不等式の評価
	// β * lhs < β*rhs + waiting*rhs
	// lhs = srtt_f * (x_f + cwnd_f*mss)
	// rhs = cwnd_f * mss * (srtt_s + delta)

	minRTTInfo := s.transports[minRTTTransport]
	availableInfo := s.transports[availableMinRTTTransport]

	srtt_f := rttToMicroseconds(minRTTInfo.SmoothedRTT())
	srtt_s := rttToMicroseconds(availableInfo.SmoothedRTT())
	rttvar_f := rttToMicroseconds(minRTTInfo.MeanDeviation())
	rttvar_s := rttToMicroseconds(availableInfo.MeanDeviation())

	cwnd_f := minRTTInfo.CongestionWindow()
	cwnd_s := availableInfo.CongestionWindow()

	// delta = max(rttvar_f, rttvar_s)
	delta := max(rttvar_f, rttvar_s)

	// x_f = max(queueSize, cwnd_f)
	x_f := max(s.queueSize, cwnd_f)

	// 第1不等式の左辺と右辺を計算
	lhs := srtt_f * (x_f + cwnd_f)
	rhs := cwnd_f * (srtt_s + delta)

	// β * lhs < β*rhs + waiting*rhs
	// これは lhs < rhs + (waiting/β)*rhs と等価
	// 簡略化: ecfBeta * lhs < ecfBeta * rhs + waiting * rhs
	betaLhs := ecfBeta * lhs
	betaRhs := ecfBeta * rhs
	waitingRhs := uint64(s.waiting) * rhs

	firstInequalityTrue := betaLhs < (betaRhs + waitingRhs)

	// 第1不等式が偽の場合、即座に availableMinRTTTransport を返す
	if !firstInequalityTrue {
		s.waiting = 0
		return availableMinRTTTransport
	}

	// 5. 第2不等式の評価 (第1が真の場合)
	// lhs_s >= rhs_s
	// lhs_s = srtt_s * x_s
	// rhs_s = cwnd_s * (2*srtt_f + delta)

	// x_s = max(queueSize, cwnd_s)
	x_s := max(s.queueSize, cwnd_s)

	lhs_s := srtt_s * x_s
	rhs_s := cwnd_s * (2*srtt_f + delta)

	secondInequalityTrue := lhs_s >= rhs_s

	// 6. 待機判定とトランスポート選択
	if secondInequalityTrue && allowWaiting {
		// 待機が有益: 最速トランスポートが利用可能になるまで待機
		s.waiting = 1
		s.waitingForTransport = minRTTTransport
		return ""
	}

	// 待機しない: 送信可能最速トランスポートを使用
	s.waiting = 0
	return availableMinRTTTransport
}
