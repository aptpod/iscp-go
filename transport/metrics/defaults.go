package metrics

import "time"

// Default values used when metrics are not yet available.
// Shared by the TCP, QUIC, and Noop implementations.
const (
	defaultRTT    = 100 * time.Millisecond
	defaultRTTVar = 50 * time.Millisecond
	defaultCWND   = 14600 // 10 * MSS (1460 bytes)
)
