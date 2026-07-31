//go:build stress

package multi_test

const (
	stressIterations = 200
	stressGoroutines = 32
	stressMode       = true
)

// stressIterationsSlow は stress_params_test.go 側のコメント参照。
const stressIterationsSlow = 20
