//go:build stress

package iscp_test

// iscp パッケージは 1 試行が重い（Connect / Close を伴う）ため、
// 他パッケージ（200）より少ない回数にする。
const (
	stressIterations = 50
	stressGoroutines = 32
	stressMode       = true
)
