//go:build stress

package iscp_test

// stressGoroutines は並行実行する goroutine 本数です。
const stressGoroutines = 32

// stressIterationsSlow は実時間依存テスト専用の試行回数です（stress_params_test.go 参照）。
// 3s × 5 = 15s 程度に抑える。
const stressIterationsSlow = 5
