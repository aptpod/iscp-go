package quic

import (
	quicgo "github.com/quic-go/quic-go"
)

// QUICConfig returns the quic-go config built from DialerConfig (for tests).
func (c DialerConfig) QUICConfig() *quicgo.Config {
	return c.quicConfig()
}
