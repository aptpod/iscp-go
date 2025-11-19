package metrics

import "time"

// MetricsProvider is an interface for retrieving transport metrics.
//
// Implementations must be safe for concurrent use.
type MetricsProvider interface {
	// RTT returns the Smoothed Round Trip Time (SRTT).
	// Returns a default value (e.g., 100ms) if not yet measured.
	RTT() time.Duration

	// RTTVar returns the RTT Variation (RTTVAR), also known as Mean Deviation.
	// Returns a default value (e.g., 50ms) if not yet measured.
	RTTVar() time.Duration

	// CongestionWindow returns the congestion window size in bytes.
	// Returns a default value (e.g., 14600 bytes = 10 * MSS) if not yet measured.
	CongestionWindow() uint64

	// BytesInFlight returns the number of bytes currently in flight
	// (sent but not yet acknowledged or completed).
	BytesInFlight() uint64
}
