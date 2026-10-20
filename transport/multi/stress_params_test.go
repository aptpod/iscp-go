//go:build !stress

package multi_test

// stressIterations is the number of iterations per test.
// Timing-dependent bugs do not show up in a single run, so even normal builds run several iterations.
// To run many more iterations, build with -tags stress (stress_params_stress_test.go).
const (
	stressIterations = 5
	stressGoroutines = 4
)

// stressIterationsSlow is only for "real-time dependent" tests that insert a fixed time.Sleep per iteration.
// Using stressIterations as is would multiply the wait time by 200 in stress builds
// and blow up the run time, so it is kept separate
// (measured on 2026-07-31: just the 3 real-time dependent giveUp/Close tests in transport/multi
// took over 10 minutes in total with stressIterations=200).
const stressIterationsSlow = 3
