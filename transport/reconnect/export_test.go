package reconnect

import (
	"github.com/aptpod/iscp-go/transport"
)

// Reconnect は、reconnect をテスト用にエクスポートします。
func (r *Transport) Reconnect(old transport.Transport) error {
	return r.reconnect(old)
}

// CurrentTransport は、現在の下層トランスポートをテスト用にエクスポートします。
func (r *Transport) CurrentTransport() transport.Transport {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.transport
}
