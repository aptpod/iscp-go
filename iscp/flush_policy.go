package iscp

import "time"

// FlushPolicyは、Upstreamのフラッシュの方法について定義します。
type FlushPolicy interface {
	// Tickerは、時間によるフラッシュを取得します。
	//
	// tickチャンネルが時間を返す度にフラッシュを行います。
	Ticker() (tick <-chan time.Time, stop func())

	// IsFlushは、内部バッファのサイズからフラッシュするかどうかを判定します。
	//
	// 内部バッファのサイズは蓄積されたデータポイントのペイロードサイズの合計値です。
	IsFlush(size uint32) bool
}

type flushPolicyNone struct{}

func (p *flushPolicyNone) Ticker() (<-chan time.Time, func()) {
	return make(chan time.Time), func() {}
}
func (p *flushPolicyNone) IsFlush(size uint32) bool { return false }

type flushPolicyIntervalOnly struct {
	Interval time.Duration
}

func (p *flushPolicyIntervalOnly) Ticker() (<-chan time.Time, func()) {
	ticker := time.NewTicker(p.Interval)
	return ticker.C, ticker.Stop
}
func (p *flushPolicyIntervalOnly) IsFlush(size uint32) bool { return false }

type flushPolicyIntervalOrBufferSize struct {
	BufferPolicy   *flushPolicyBufferSizeOnly
	IntervalPolicy *flushPolicyIntervalOnly
}

func (p *flushPolicyIntervalOrBufferSize) Ticker() (<-chan time.Time, func()) {
	return p.IntervalPolicy.Ticker()
}

func (p *flushPolicyIntervalOrBufferSize) IsFlush(size uint32) bool {
	return p.BufferPolicy.IsFlush(size)
}

type flushPolicyBufferSizeOnly struct {
	BufferSize uint32
}

func (p *flushPolicyBufferSizeOnly) Ticker() (<-chan time.Time, func()) {
	return make(chan time.Time), func() {}
}
func (p *flushPolicyBufferSizeOnly) IsFlush(size uint32) bool { return size > p.BufferSize }

type flushPolicyImmediately struct{}

func (p *flushPolicyImmediately) Ticker() (<-chan time.Time, func()) {
	return make(chan time.Time), func() {}
}
func (p *flushPolicyImmediately) IsFlush(bufferSize uint32) bool { return true }
