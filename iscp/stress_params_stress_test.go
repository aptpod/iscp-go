//go:build stress

package iscp_test

// iscp パッケージは 1 試行が重い（Connect / Close を伴う）ため、
// 他パッケージ（200）より少ない回数にする。
const (
	stressIterations = 50
	stressGoroutines = 32
	stressMode       = true
)

// stressIterationsSlow は実時間依存テスト専用の試行回数です（stress_params_test.go 参照）。
// 3s × 5 = 15s 程度に抑える。
const stressIterationsSlow = 5
