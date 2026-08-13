package webtransport

import (
	quicgo "github.com/quic-go/quic-go"
)

// QUICConfigは、DialerConfigから組み立てたquic-goの設定を返却します（テスト用）。
func (c DialerConfig) QUICConfig() *quicgo.Config {
	return c.quicConfig()
}
