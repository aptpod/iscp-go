package multi

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeClock はテスト用の単調増加クロック。
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Unix(0, 0)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func TestNoConnectedTracker_閾値が0以下なら常にfalse(t *testing.T) {
	for _, timeout := range []time.Duration{0, -time.Second} {
		clock := newFakeClock()
		tr := newNoConnectedTracker(timeout)
		tr.now = clock.Now

		require.False(t, tr.observe(false))
		clock.Advance(24 * time.Hour)
		require.False(t, tr.observe(false), "無効化されているので何時間経っても畳まない")
	}
}

func TestNoConnectedTracker_閾値を超えたらtrueを返す(t *testing.T) {
	clock := newFakeClock()
	tr := newNoConnectedTracker(100 * time.Millisecond)
	tr.now = clock.Now

	// 最初の観測は記録のみ。
	require.False(t, tr.observe(false))

	clock.Advance(99 * time.Millisecond)
	require.False(t, tr.observe(false), "閾値未満ではまだ畳まない")

	clock.Advance(1 * time.Millisecond)
	require.True(t, tr.observe(false), "閾値ちょうどで畳む")
}

func TestNoConnectedTracker_接続が戻ったら計測をリセットする(t *testing.T) {
	clock := newFakeClock()
	tr := newNoConnectedTracker(100 * time.Millisecond)
	tr.now = clock.Now

	require.False(t, tr.observe(false))
	clock.Advance(90 * time.Millisecond)

	// 1 本でも Connected になったらリセット。
	require.False(t, tr.observe(true))

	clock.Advance(90 * time.Millisecond)
	require.False(t, tr.observe(false), "リセット後の初回観測は記録のみ")

	clock.Advance(99 * time.Millisecond)
	require.False(t, tr.observe(false), "リセット後は改めて閾値ぶん待つ")

	clock.Advance(1 * time.Millisecond)
	require.True(t, tr.observe(false))
}

// level-trigger であること: 状態が変化しない observe(false) の連打だけで
// 閾値超過を検出できる（遷移イベントを必要としない）。
func TestNoConnectedTracker_遷移がなくても検出する(t *testing.T) {
	clock := newFakeClock()
	tr := newNoConnectedTracker(50 * time.Millisecond)
	tr.now = clock.Now

	require.False(t, tr.observe(false))
	for i := 0; i < 4; i++ {
		clock.Advance(10 * time.Millisecond)
		require.False(t, tr.observe(false))
	}
	clock.Advance(10 * time.Millisecond)
	require.True(t, tr.observe(false))
}

// 閾値超過後も観測を続けたら true を返し続ける（呼び出し側で多重 teardown を防ぐ前提）。
func TestNoConnectedTracker_超過後も繰り返しtrueを返す(t *testing.T) {
	clock := newFakeClock()
	tr := newNoConnectedTracker(10 * time.Millisecond)
	tr.now = clock.Now

	require.False(t, tr.observe(false))
	clock.Advance(10 * time.Millisecond)
	require.True(t, tr.observe(false))
	require.True(t, tr.observe(false))
}

func TestNoConnectedTracker_並行呼び出しでも壊れない(t *testing.T) {
	tr := newNoConnectedTracker(time.Hour)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				tr.observe(j%2 == 0)
			}
		}(i)
	}
	wg.Wait()
	// -race で検出させるのが目的。ここでは panic しないことだけ確認する。
}
