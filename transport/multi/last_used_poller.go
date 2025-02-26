package multi

import "github.com/aptpod/iscp-go/transport"

var (
	_ Poller               = (*LastUsedPoller)(nil)
	_ MultiTransportSetter = (*LastUsedPoller)(nil)
)

// LastUsedPoller は、TransportIDを順番に返すPollerの実装です。
type LastUsedPoller struct {
	tr *Transport
}

// NewLastReadPoller は新しいLastUsedPollerを作成します。
func NewLastReadPoller() *LastUsedPoller {
	return &LastUsedPoller{}
}

func (p *LastUsedPoller) SetMultiTransport(tr *Transport) {
	p.tr = tr
}

// Get は次のTransportIDを返します。
func (p *LastUsedPoller) Get() transport.TransportID {
	if p.tr == nil {
		return p.tr.currentTransportID
	}
	p.tr.lastReadTransportIDmu.RLock()
	defer p.tr.lastReadTransportIDmu.RUnlock()
	tID := p.tr.lastReadTransportID
	if tID != "" {
		return p.tr.currentTransportID
	}
	return p.tr.lastReadTransportID
}
