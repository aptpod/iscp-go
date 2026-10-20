package metrics

import "time"

var _ ManagedMetricsProvider = (*noopMetricsProvider)(nil)

// noopMetricsProvider is a do-nothing implementation of ManagedMetricsProvider.
// It is used in environments where metrics are unavailable (non-Linux, no TCP connection, etc.).
//
// This implementation follows the Null Object Pattern: every method either returns
// default values or does nothing. This removes the need for nil checks in callers.
type noopMetricsProvider struct{}

// NewNopMetricsProvider は新しい noopMetricsProvider を作成します。
//
// この実装は以下の場合に使用されます：
//   - 非Linux環境（TCP_INFO syscall が利用できない）
//   - TCP接続が抽出できない場合（例：WebSocketではない、またはリフレクション失敗）
//   - メトリクス収集が不要な場合
func NewNopMetricsProvider() ManagedMetricsProvider {
	return &noopMetricsProvider{}
}

// RTT returns the default RTT value.
func (n *noopMetricsProvider) RTT() time.Duration {
	return defaultRTT
}

// RTTVar returns the default RTT variation value.
func (n *noopMetricsProvider) RTTVar() time.Duration {
	return defaultRTTVar
}

// CongestionWindow returns the default congestion window size.
func (n *noopMetricsProvider) CongestionWindow() uint64 {
	return defaultCWND
}

// BytesInFlight always returns 0 since no actual tracking is performed.
func (n *noopMetricsProvider) BytesInFlight() uint64 {
	return 0
}

// Start is a no-op and always succeeds.
func (n *noopMetricsProvider) Start() error {
	return nil // No-op, always succeeds
}

// Stop is a no-op.
func (n *noopMetricsProvider) Stop() {
	// No-op
}
