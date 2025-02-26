package multi

import "github.com/aptpod/iscp-go/transport"

func (t *Transport) CurrentTransportID() transport.TransportID {
	return t.currentTransportID
}
