//go:build !stress

package multi_test

// stressIterations は 1 テストあたりの試行回数です。
// タイミング依存の不具合は 1 回では出ないため、通常ビルドでも複数回まわします。
// 多数回まわしたいときは -tags stress でビルドしてください（stress_params_stress_test.go）。
const (
	stressIterations = 5
	stressGoroutines = 4
	stressMode       = false
)
