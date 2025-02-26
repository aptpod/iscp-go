package multi

import (
	"sync"

	"github.com/aptpod/iscp-go/transport"
)

// RoundRobinPoller は、TransportIDを順番に返すPollerの実装です。
type RoundRobinPoller struct {
	transportIDs []transport.TransportID
	current      int
	mu           sync.Mutex
}

// NewRoundRobinPoller は新しいRoundRobinPollerを作成します。
func NewRoundRobinPoller(transportIDs []transport.TransportID) *RoundRobinPoller {
	return &RoundRobinPoller{
		transportIDs: transportIDs,
		current:      0,
	}
}

// Get は次のTransportIDを返します。
func (p *RoundRobinPoller) Get() transport.TransportID {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.transportIDs) == 0 {
		return ""
	}

	id := p.transportIDs[p.current]
	p.current = (p.current + 1) % len(p.transportIDs)
	return id
}
