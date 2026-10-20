//go:build stress

package multi_test

const (
	stressIterations = 200
	stressGoroutines = 32
)

// stressIterationsSlow: see the comment in stress_params_test.go.
const stressIterationsSlow = 20
