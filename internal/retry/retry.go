package retry

import (
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
		if r.MaxAttempt != 0 && retryCount > r.MaxAttempt {
			return
		}
		if f() {
			return
		}
		time.Sleep(nextSleep(retryCount, baseInterval, maxBaseInterval))
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
