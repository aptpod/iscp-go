//go:build !stress

package iscp_test

// stressIterations は 1 テストあたりの試行回数です。
// タイミング依存の不具合は 1 回では出ないため、通常ビルドでも複数回まわします。
// 多数回まわしたいときは -tags stress でビルドしてください（stress_params_stress_test.go）。
const (
	stressIterations = 5
	stressGoroutines = 4
	stressMode       = false
)

// stressIterationsSlow は、1 周回に固定の time.Sleep を挟む「実時間依存」テスト専用の
// 試行回数です。TestConn_Close_TimesOutWhenDisconnectSendBlocks_繰り返し は毎回
// disconnectSendTimeout（3s）ぶん待つため、stressIterations をそのまま使うと
// 待ち時間が単純に膨れます。
const stressIterationsSlow = 2
