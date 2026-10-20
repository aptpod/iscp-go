//go:build !stress

package reconnect_test

// stressIterations is the number of attempts per test.
// Timing-dependent bugs do not appear in a single attempt, so even the normal build runs multiple iterations.
// To run many more iterations, build with -tags stress (stress_params_stress_test.go).
const (
	stressIterations = 5
	stressGoroutines = 4
	stressMode       = false
)
