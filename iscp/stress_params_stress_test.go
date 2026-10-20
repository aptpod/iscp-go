//go:build stress

package iscp_test

// stressGoroutines is the number of goroutines to run concurrently.
const stressGoroutines = 32

// stressIterationsSlow is the iteration count for wall-clock dependent tests only (see stress_params_test.go).
// Keep it to about 3s × 5 = 15s.
const stressIterationsSlow = 5
