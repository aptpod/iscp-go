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

// stressIterationsSlow は、1 周回に固定の time.Sleep を挟む「実時間依存」テスト専用の
// 試行回数です。stressIterations をそのまま使うと stress ビルドで待ち時間が単純に
// 200 倍になり実行時間が膨れ上がるため、別枠にしています
// （2026-07-31 実測: transport/multi の giveUp/Close 系実時間依存テスト 3 本だけで
// stressIterations=200 のままだと合計 10 分超に達した）。
const stressIterationsSlow = 3
