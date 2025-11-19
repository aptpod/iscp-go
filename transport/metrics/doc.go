// Package metrics provides transport metrics collection interfaces and implementations.
//
// # MetricsProvider
//
// MetricsProvider is an interface for retrieving transport metrics such as RTT, RTTVAR,
// congestion window, and bytes in flight. These metrics are used by advanced schedulers
// like ECF (Earliest Completion First) to make optimal transport selection decisions.
//
// # Implementations
//
// TCPInfoProvider (Linux only):
//   - Retrieves metrics directly from the kernel via TCP_INFO syscall
//   - Provides accurate Smoothed RTT, RTTVAR, and Congestion Window
//   - Periodically updates metrics in the background (default: 100ms)
//   - Manages bytesInFlight at the application layer
//
// # Future Extensions
//
// The MetricsProvider interface is designed to support multiple implementations:
//   - Ping/Pong-based metrics (cross-platform)
//   - QUIC transport metrics (using quic-go statistics)
//   - WebTransport API metrics
//   - Platform-specific syscalls (e.g., macOS SO_STAT)
package metrics
