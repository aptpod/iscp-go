package metrics

import "time"

// メトリクスがまだ利用できない場合のデフォルト値。
// TCP 実装 / QUIC 実装 / Noop 実装で共通利用される。
const (
	defaultRTT    = 100 * time.Millisecond
	defaultRTTVar = 50 * time.Millisecond
	defaultCWND   = 14600 // 10 * MSS (1460 バイト)
)
