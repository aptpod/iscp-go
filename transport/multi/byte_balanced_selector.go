package multi

import (
	"context"
	"maps"
	"sync"
	"sync/atomic"

	"github.com/aptpod/iscp-go/transport"
)

// TxBytesFunc は SubConnectionID の累積送信バイト数を返す関数型。
type TxBytesFunc func(transport.SubConnectionID) uint64

// ByteBalancedState は ByteBalanced アルゴリズムの呼び出し間で保持する状態。
type ByteBalancedState struct {
	LastSelected transport.SubConnectionID

	// 統計カウンタ
	TotalSelections   atomic.Uint64
	SwitchCount       atomic.Uint64
	SelectionCountsMu sync.Mutex
	SelectionCounts   map[transport.SubConnectionID]uint64
}

// NewByteBalancedState はデフォルト値で初期化された ByteBalancedState を返す。
func NewByteBalancedState() *ByteBalancedState {
	return &ByteBalancedState{
		SelectionCounts: make(map[transport.SubConnectionID]uint64),
	}
}

// SelectTransportByteBalanced は送信バイト数が最小のトランスポートを選択する。
// transportIDs のうち getTxBytes が最小値を返すものを選択する。
//
// state: 統計記録用の状態（nil の場合は統計を記録しない）
func SelectTransportByteBalanced(
	transportIDs []transport.SubConnectionID,
	getTxBytes TxBytesFunc,
	state *ByteBalancedState,
) transport.SubConnectionID {
	if len(transportIDs) == 0 {
		return ""
	}

	var selectedID transport.SubConnectionID
	minBytes := ^uint64(0)

	for _, id := range transportIDs {
		b := getTxBytes(id)
		if b < minBytes {
			minBytes = b
			selectedID = id
		}
	}

	if selectedID != "" && state != nil {
		state.TotalSelections.Add(1)
		if state.LastSelected != "" && state.LastSelected != selectedID {
			state.SwitchCount.Add(1)
		}
		state.LastSelected = selectedID
		state.SelectionCountsMu.Lock()
		state.SelectionCounts[selectedID]++
		state.SelectionCountsMu.Unlock()
	}

	return selectedID
}

// ByteBalancedSelector は、送信バイト数に基づいてトランスポートを選択する TransportSelector の実装です。
// 各トランスポートの累積送信バイト数（TxBytesCounterValue）を追跡し、
// 最も送信量が少ないトランスポートを優先的に選択することで、複数トランスポート間の送信負荷を均等化します。
//
// MultiTransportSetter インターフェースを実装しており、マルチトランスポートへの参照が設定されると、
// 選択されたトランスポートが利用可能か確認し、必要に応じてフォールバックします。
type ByteBalancedSelector struct {
	transportIDs   []transport.SubConnectionID
	mu             sync.RWMutex
	multiTransport *Transport

	// 統計情報
	stateMu               sync.Mutex
	lastSelectedTransport transport.SubConnectionID
	totalSelections       atomic.Uint64
	switchCount           atomic.Uint64

	selectionCountsMu sync.Mutex
	selectionCounts   map[transport.SubConnectionID]uint64
}

// ByteBalancedStats は ByteBalancedSelector の統計情報を保持します。
type ByteBalancedStats struct {
	// SelectionCounts はトランスポートごとの選択回数です。
	SelectionCounts map[transport.SubConnectionID]uint64
	// TotalSelections は総選択回数です。
	TotalSelections uint64
	// SwitchCount はトランスポート切り替え回数です。
	SwitchCount uint64
}

// NewByteBalancedSelector は新しい ByteBalancedSelector を作成します。
// transportIDs は選択対象となるトランスポートIDのリストです。
// 同一送信バイト数の場合、このリストの順序で優先されます。
func NewByteBalancedSelector(transportIDs []transport.SubConnectionID) *ByteBalancedSelector {
	return &ByteBalancedSelector{
		transportIDs:    transportIDs,
		selectionCounts: make(map[transport.SubConnectionID]uint64),
	}
}

// SetMultiTransport は管理対象のマルチトランスポートへの参照を設定します。
// MultiTransportSetter インターフェースを実装します。
//
// この参照が設定されると、Get() は選択されたトランスポートが利用可能か確認し、
// 必要に応じてフォールバックします。
func (s *ByteBalancedSelector) SetMultiTransport(mt *Transport) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.multiTransport = mt
}

// Get は送信バイト数が最小のトランスポートの SubConnectionID を返します。
// bsSize パラメータは無視されます。
//
// multiTransport が設定されている場合、選択されたトランスポートが利用可能か確認し、
// 利用不可の場合は他の利用可能なトランスポートにフォールバックします。
func (s *ByteBalancedSelector) Get(_ context.Context, _ int64) transport.SubConnectionID {
	s.mu.RLock()
	mt := s.multiTransport
	s.mu.RUnlock()

	selectedID := s.selectMinTxBytes(mt)

	if mt == nil {
		return selectedID
	}

	// 選択されたトランスポートが利用可能か確認し、必要に応じてフォールバック
	return SelectAvailableTransport(selectedID, mt.Transports())
}

// selectMinTxBytes は最小送信バイト数を持つトランスポートを選択します。
func (s *ByteBalancedSelector) selectMinTxBytes(mt *Transport) transport.SubConnectionID {
	s.mu.RLock()
	transportIDs := s.transportIDs
	s.mu.RUnlock()

	if len(transportIDs) == 0 {
		return ""
	}

	// multiTransport が未設定の場合、最初のトランスポートを返す
	if mt == nil {
		selectedID := transportIDs[0]
		s.recordSelection(selectedID)
		return selectedID
	}

	transports := mt.Transports()
	if len(transports) == 0 {
		return ""
	}

	// transports に存在する transportIDs のみフィルタ
	filtered := make([]transport.SubConnectionID, 0, len(transportIDs))
	for _, id := range transportIDs {
		if _, exists := transports[id]; exists {
			filtered = append(filtered, id)
		}
	}

	selectedID := SelectTransportByteBalanced(filtered, func(id transport.SubConnectionID) uint64 {
		return transports[id].TxBytesCounterValue()
	}, nil) // state=nil: 統計は ByteBalancedSelector 自身で管理

	if selectedID != "" {
		s.recordSelection(selectedID)
	}

	return selectedID
}

// recordSelection は選択結果を記録します。
func (s *ByteBalancedSelector) recordSelection(selectedID transport.SubConnectionID) {
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
func (s *ByteBalancedSelector) Stats() ByteBalancedStats {
	s.selectionCountsMu.Lock()
	countsCopy := maps.Clone(s.selectionCounts)
	s.selectionCountsMu.Unlock()

	return ByteBalancedStats{
		SelectionCounts: countsCopy,
		TotalSelections: s.totalSelections.Load(),
		SwitchCount:     s.switchCount.Load(),
	}
}

// ResetStats は統計情報をリセットします。
func (s *ByteBalancedSelector) ResetStats() {
	s.totalSelections.Store(0)
	s.switchCount.Store(0)

	s.selectionCountsMu.Lock()
	s.selectionCounts = make(map[transport.SubConnectionID]uint64)
	s.selectionCountsMu.Unlock()

	s.stateMu.Lock()
	s.lastSelectedTransport = ""
	s.stateMu.Unlock()
}
