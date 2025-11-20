package multi

import (
	"sync"

	"github.com/aptpod/iscp-go/transport"
)

// RoundRobinSelector は、TransportIDを順番に返すTransportSelectorの実装です。
// Get()が呼ばれるたびに、次のTransportIDをラウンドロビン方式で返します。
type RoundRobinSelector struct {
	transportIDs []transport.TransportID
	current      int
	mu           sync.Mutex
}

// NewRoundRobinSelector は新しいRoundRobinSelectorを作成します。
func NewRoundRobinSelector(transportIDs []transport.TransportID) *RoundRobinSelector {
	return &RoundRobinSelector{
		transportIDs: transportIDs,
		current:      0,
	}
}

// Get は次のTransportIDを返します。
// bsSizeパラメータは無視され、単純にラウンドロビン方式で次のTransportIDを返します。
func (s *RoundRobinSelector) Get(bsSize int64) transport.TransportID {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.transportIDs) == 0 {
		return ""
	}

	id := s.transportIDs[s.current]
	s.current = (s.current + 1) % len(s.transportIDs)
	return id
}
