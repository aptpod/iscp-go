package multi

import (
	"maps"
	"sync"
	"sync/atomic"

	"github.com/aptpod/iscp-go/log"
	"github.com/aptpod/iscp-go/transport"
)

// MinRTTSelector は MinRTT (Minimum RTT) アルゴリズムを実装した TransportSelector です。
// ECFスケジューラとは異なり、待機判定を行わず、その時点で利用可能なトランスポートの中から
// MinRTT（ベースRTT）が最小のものを即座に選択します。
// これにより、待機なしの低レイテンシ通信を実現します。
type MinRTTSelector struct {
	transportsMu   sync.RWMutex
	multiTransport *Transport
	transports     map[transport.TransportID]*TransportInfo

	stateMu               sync.Mutex
	lastSelectedTransport transport.TransportID
	logger                log.Logger

	// 統計情報（atomic操作で保護、ロック不要）
	totalSelections atomic.Uint64
	switchCount     atomic.Uint64

	selectionCountsMu sync.Mutex
	selectionCounts   map[transport.TransportID]uint64
}

// MinRTTStats は MinRTTSelector の統計情報を保持します。
type MinRTTStats struct {
	SelectionCounts map[transport.TransportID]uint64
	TotalSelections uint64
	SwitchCount     uint64
}

// NewMinRTTSelector は新しい MinRTTSelector を作成します。
func NewMinRTTSelector() *MinRTTSelector {
	return &MinRTTSelector{
		transports:      make(map[transport.TransportID]*TransportInfo),
		selectionCounts: make(map[transport.TransportID]uint64),
		logger:          log.NewNop(),
	}
}

// SetLogger はロガーを設定します。TransportMetricsUpdater を実装します。
func (s *MinRTTSelector) SetLogger(logger log.Logger) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.logger = logger
}

// SetMultiTransport はマルチトランスポートへの参照を設定します。
func (s *MinRTTSelector) SetMultiTransport(mt *Transport) {
	s.transportsMu.Lock()
	defer s.transportsMu.Unlock()
	s.multiTransport = mt
}

// UpdateTransport はトランスポートのメトリクス情報を更新します。
func (s *MinRTTSelector) UpdateTransport(transportID transport.TransportID, info *TransportInfo) {
	s.transportsMu.Lock()
	defer s.transportsMu.Unlock()

	if existingInfo, exists := s.transports[transportID]; exists && existingInfo.minRTT > 0 {
		info.minRTT = existingInfo.minRTT
	}
	info.Update()
	s.transports[transportID] = info
}

// SetQueueSize は TransportMetricsUpdater インターフェースを満たすための no-op 実装です。
// MinRTTSelector では待機判定を行わないため、キューサイズは使用しません。
func (s *MinRTTSelector) SetQueueSize(_ uint64) {
	// no-op: MinRTTSelector does not use queue size
}

// Get は指定されたデータサイズに基づいて最適な TransportID を返します。
// MinRTTSelector は待機なしで、利用可能なトランスポートの中からMinRTTが最小のものを即座に選択します。
func (s *MinRTTSelector) Get(_ int64) transport.TransportID {
	selectedID := s.selectTransportMinRTT()

	// マルチトランスポートから実際に利用可能なトランスポートを選択
	s.transportsMu.RLock()
	mt := s.multiTransport
	s.transportsMu.RUnlock()

	if mt == nil {
		return selectedID
	}

	return SelectAvailableTransport(selectedID, mt.Transports())
}

// selectTransportMinRTT はMinRTTアルゴリズムに基づいてトランスポートを選択します。
func (s *MinRTTSelector) selectTransportMinRTT() transport.TransportID {
	s.transportsMu.RLock()
	defer s.transportsMu.RUnlock()

	numTransports := len(s.transports)

	// エッジケース: トランスポートがない
	if numTransports == 0 {
		return ""
	}

	// エッジケース: トランスポートが1つのみ
	if numTransports == 1 {
		for id := range s.transports {
			s.recordSelection(id)
			return id
		}
	}

	// 送信可能なトランスポートを優先しつつ、フォールバック候補も同時に追跡
	var selectedID transport.TransportID
	var fallbackID transport.TransportID
	minRTT := ^uint64(0)
	minSmoothedRTT := ^uint64(0)
	fallbackMinRTT := ^uint64(0)
	fallbackSmoothedRTT := ^uint64(0)

	for id, info := range s.transports {
		currentMinRTT := rttToMicroseconds(info.MinRTT())
		currentSmoothedRTT := rttToMicroseconds(info.SmoothedRTT())

		// フォールバック候補を常に更新（全トランスポートの中でMinRTT最小）
		if currentMinRTT < fallbackMinRTT ||
			(currentMinRTT == fallbackMinRTT && currentSmoothedRTT < fallbackSmoothedRTT) {
			fallbackID = id
			fallbackMinRTT = currentMinRTT
			fallbackSmoothedRTT = currentSmoothedRTT
		}

		// 送信可能なトランスポートを優先
		if info.SendingAllowed() {
			if currentMinRTT < minRTT ||
				(currentMinRTT == minRTT && currentSmoothedRTT < minSmoothedRTT) {
				selectedID = id
				minRTT = currentMinRTT
				minSmoothedRTT = currentSmoothedRTT
			}
		}
	}

	// 送信可能なトランスポートがなければフォールバック
	if selectedID == "" {
		selectedID = fallbackID
	}

	if selectedID != "" {
		s.recordSelection(selectedID)
	}

	return selectedID
}

// recordSelection は選択結果を記録します。
func (s *MinRTTSelector) recordSelection(selectedID transport.TransportID) {
	s.totalSelections.Add(1)

	s.stateMu.Lock()
	lastSelected := s.lastSelectedTransport
	if lastSelected != "" && lastSelected != selectedID {
		s.switchCount.Add(1)
	}
	s.lastSelectedTransport = selectedID
	s.stateMu.Unlock()

	s.selectionCountsMu.Lock()
	s.selectionCounts[selectedID]++
	s.selectionCountsMu.Unlock()
}

// Stats は現在の統計情報のスナップショットを返します。
func (s *MinRTTSelector) Stats() MinRTTStats {
	s.selectionCountsMu.Lock()
	countsCopy := maps.Clone(s.selectionCounts)
	s.selectionCountsMu.Unlock()

	return MinRTTStats{
		SelectionCounts: countsCopy,
		TotalSelections: s.totalSelections.Load(),
		SwitchCount:     s.switchCount.Load(),
	}
}

// ResetStats は統計情報をリセットします。
func (s *MinRTTSelector) ResetStats() {
	s.totalSelections.Store(0)
	s.switchCount.Store(0)

	s.selectionCountsMu.Lock()
	s.selectionCounts = make(map[transport.TransportID]uint64)
	s.selectionCountsMu.Unlock()

	s.stateMu.Lock()
	s.lastSelectedTransport = ""
	s.stateMu.Unlock()
}
