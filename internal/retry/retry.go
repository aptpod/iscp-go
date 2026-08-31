package retry

import (
	"context"
	"math"
	"math/rand"
	"time"
)

var (
	randFloat64         = rand.Float64
	defaultBaseInterval = 100 * time.Millisecond
	defaultMaxInterval  = 5 * time.Second
)

// RetryはExponential Backoff and Jitter方式のリトライを行います。
//
// Jitterは 0.5 ~ 1.5のランダム値です。
type Retry struct {
	// 最大試行回数。0はリトライをし続けます。デフォルトは0です。
	MaxAttempt int

	// 基準リトライ間隔。デフォルトは100ミリ秒です。
	BaseInterval time.Duration

	// 最大基準リトライ間隔。デフォルトは5秒です。
	MaxBaseInterval time.Duration
}

// RetryFuncは、リトライを実施する関数です。
type RetryFunc func() (end bool)

func (r Retry) Do(f RetryFunc) {
	r.DoWithContext(context.Background(), f)
}

// DoWithContextは、ctxのキャンセルまでリトライを行います。
//
// キャンセルはリトライ間隔のスリープ中にも即座に反映されます。また、
// キャンセル済みの場合は f を追加で呼び出さずに戻ります（実行中の f は
// 中断しません。f 自体の中断は f 側でctxを見る必要があります）。
func (r Retry) DoWithContext(ctx context.Context, f RetryFunc) {
	baseInterval := r.BaseInterval
	if baseInterval == 0 {
		baseInterval = defaultBaseInterval
	}
	maxBaseInterval := r.MaxBaseInterval
	if maxBaseInterval == 0 {
		maxBaseInterval = defaultMaxInterval
	}
	var retryCount int
	for {
		if ctx.Err() != nil {
			return
		}
		if r.MaxAttempt != 0 && retryCount > r.MaxAttempt {
			return
		}
		if f() {
			return
		}
		timer := time.NewTimer(nextSleep(retryCount, baseInterval, maxBaseInterval))
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return
		}
		retryCount++
	}
}

func nextSleep(count int, base, max time.Duration) time.Duration {
	baseInterval := float64(base) * math.Pow(2, float64(count))
	if baseInterval > float64(max) {
		baseInterval = float64(max)
	}

	jitter := 0.5 + randFloat64()
	return time.Duration(baseInterval * jitter)
}

func Do(f RetryFunc) {
	retry := Retry{}
	retry.Do(f)
}

// DoWithContextは、デフォルト設定でctxのキャンセルまでリトライを行います。
func DoWithContext(ctx context.Context, f RetryFunc) {
	retry := Retry{}
	retry.DoWithContext(ctx, f)
}
