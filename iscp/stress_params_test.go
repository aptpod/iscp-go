//go:build !stress

package iscp_test

// stressGoroutines は並行実行する goroutine 本数です。
const stressGoroutines = 4

// stressIterationsSlow は、1 周回に固定の time.Sleep を挟む「実時間依存」テスト専用の
// 試行回数です。TestConn_Close_TimesOutWhenDisconnectSendBlocks_繰り返し は毎回
// disconnectSendTimeout（3s）ぶん待つため、stressIterations をそのまま使うと
// 待ち時間が単純に膨れます。
const stressIterationsSlow = 2
