package reconnect

// Statusは接続の状態を表します。
//
// - StatusConnecting
// - StatusConnected
// - StatusReconnecting
// - StatusDisconnected
type Status int

// StatusConnecting は初期接続中の状態を表します。
// StatusConnected は接続が確立された状態を表します。
// StatusReconnecting は再接続中の状態を表します。
// StatusDisconnected は切断された状態を表します。
const (
	// StatusConnected 接続済み (値は既存の動作を維持するため0)
	StatusConnected Status = 0
	// StatusReconnecting 再接続中
	StatusReconnecting Status = 1
	// StatusDisconnected 切断
	StatusDisconnected Status = 2
	// StatusConnecting 初期接続中
	StatusConnecting Status = 3
)

// StatusProviderは接続の状態を取得するインターフェースです。
type StatusProvider interface {
	Status() Status
}
