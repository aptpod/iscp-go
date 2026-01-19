package multi

import (
	"sync"

	"github.com/aptpod/iscp-go/transport"
)

// RoundRobinSelector は、TransportIDを順番に返すTransportSelectorの実装です。
// Get()が呼ばれるたびに、次のTransportIDをラウンドロビン方式で返します。
//
// MultiTransportSetterを実装しており、マルチトランスポートへの参照が設定されると、
// 選択されたトランスポートが利用可能か確認し、必要に応じてフォールバックします。
type RoundRobinSelector struct {
	transportIDs   []transport.TransportID
	current        int
	mu             sync.Mutex
	multiTransport *Transport
}

// NewRoundRobinSelector は新しいRoundRobinSelectorを作成します。
func NewRoundRobinSelector(transportIDs []transport.TransportID) *RoundRobinSelector {
	return &RoundRobinSelector{
		transportIDs: transportIDs,
		current:      0,
	}
}

// SetMultiTransport は管理対象のマルチトランスポートへの参照を設定します。
// MultiTransportSetterインターフェースを実装します。
//
// この参照が設定されると、Get()は選択されたトランスポートが利用可能か確認し、
// 必要に応じてフォールバックします。
func (s *RoundRobinSelector) SetMultiTransport(mt *Transport) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.multiTransport = mt
}

// Get は次のTransportIDを選択し、利用可能であることを確認します。
// bsSizeパラメータは無視され、単純にラウンドロビン方式で次のTransportIDを選択します。
//
// multiTransportが設定されている場合、選択されたトランスポートが利用可能か確認し、
// 利用不可の場合は他の利用可能なトランスポートにフォールバックします。
func (s *RoundRobinSelector) Get(bsSize int64) transport.TransportID {
	s.mu.Lock()
	selectedID := s.selectNext()
	mt := s.multiTransport
	s.mu.Unlock()

	if mt == nil {
		return selectedID
	}

	// 選択されたトランスポートが利用可能か確認し、必要に応じてフォールバック
	return SelectAvailableTransport(selectedID, mt.Transports())
}

// selectNext は内部のラウンドロビン選択ロジック（ロックは呼び出し元で取得済みと仮定）。
func (s *RoundRobinSelector) selectNext() transport.TransportID {
	if len(s.transportIDs) == 0 {
		return ""
	}

	id := s.transportIDs[s.current]
	s.current = (s.current + 1) % len(s.transportIDs)
	return id
}
