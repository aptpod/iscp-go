package metrics

import "time"

var _ ManagedMetricsProvider = (*noopMetricsProvider)(nil)

// noopMetricsProvider は ManagedMetricsProvider の何もしない実装です。
// メトリクスが利用できない環境（非Linux、TCP接続なし等）で使用されます。
//
// この実装は Null Object Pattern に従い、すべてのメソッドがデフォルト値を返すか
// 何もしない動作をします。これにより、呼び出し側で nil チェックが不要になります。
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
	return 100 * time.Millisecond // デフォルト値
}

// RTTVar returns the default RTT variation value.
func (n *noopMetricsProvider) RTTVar() time.Duration {
	return 50 * time.Millisecond // デフォルト値
}

// CongestionWindow returns the default congestion window size.
func (n *noopMetricsProvider) CongestionWindow() uint64 {
	return 14600 // 10 * MSS (1460 bytes)
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
