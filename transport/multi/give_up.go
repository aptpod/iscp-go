package multi

import (
	"sync"
	"time"
)

// Copy of the defaults of reconnect.Dial.
//
// The values are copied here because the reconnect package is intentionally left unchanged,
// so a public constant cannot be added. The source is transport/reconnect/transport.go:138-143.
// Any drift is detected by
// TestCalcNoConnectedTransportTimeout_MatchesReconnectDefaults in give_up_defaults_test.go.
const (
	defaultMaxReconnectAttempts = 30
	defaultReconnectInterval    = time.Second
)

// CalcNoConnectedTransportTimeout は、reconnect.Transport 向けの
// MaxReconnectAttempts / ReconnectInterval 設定から、multi.Transport が
// 全体を諦めるまでの猶予時間（TransportConfig.NoConnectedTransportTimeout）を算出します。
//
// multi.Transport を使う構成では、各 sub-connection には無期限リトライ（-1）を
// させたうえで、全体の生死判定を親である multi.Transport に集約します。
// 本関数は、従来 sub-connection ごとに解釈されていた設定値を、その集約後の
// 猶予時間へ読み替えるためのものです。
//
//   - maxReconnectAttempts < 0（無期限）: 0 を返す。multi.Transport は全体を畳まない
//   - maxReconnectAttempts == 0（未設定）: 既定回数 30 回ぶん
//   - maxReconnectAttempts > 0: その回数ぶん
//
// reconnectInterval が 0 以下の場合は reconnect.Dial と同じ既定値（1 秒）を用います。
func CalcNoConnectedTransportTimeout(maxReconnectAttempts int, reconnectInterval time.Duration) time.Duration {
	if maxReconnectAttempts < 0 {
		return 0
	}
	if reconnectInterval <= 0 {
		reconnectInterval = defaultReconnectInterval
	}
	attempts := maxReconnectAttempts
	if attempts == 0 {
		attempts = defaultMaxReconnectAttempts
	}
	return time.Duration(attempts) * reconnectInterval
}

// noConnectedTracker tracks how long there has been no connected sub-connection
// and detects when the duration exceeds the threshold.
//
// Implementation requirements:
//   - level-trigger: judge from the current observation on every call. Relying on
//     state transition events would miss a silent failure where no further transition
//     arrives after a false clear.
//   - check-and-set under a single lock: if reading the observation and writing the record
//     are separated, reversed interleaving between multiple status callbacks and the ticker
//     causes false clears or false residue.
//   - monotonic clock: use time.Now() / time.Since(). Comparing Unix times
//     gives wrong results when the clock is set back.
type noConnectedTracker struct {
	mu      sync.Mutex
	timeout time.Duration
	// since is the time when there became no connected sub-connection. The zero value means not recorded.
	since time.Time
	// now is a time function that tests can replace.
	now func() time.Time
}

// newNoConnectedTracker creates a tracker. When timeout is 0 or less,
// observe always returns false (that is, it never gives up).
func newNoConnectedTracker(timeout time.Duration) *noConnectedTracker {
	return &noConnectedTracker{
		timeout: timeout,
		now:     time.Now,
	}
}

// observe records the current observation and returns true if the state of having
// no connected sub-connection has continued beyond the threshold.
//
// hasConnected reports whether there is at least one connected (StatusConnected) sub-connection.
func (t *noConnectedTracker) observe(hasConnected bool) bool {
	if t.timeout <= 0 {
		return false
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if hasConnected {
		t.since = time.Time{}
		return false
	}
	if t.since.IsZero() {
		t.since = t.now()
		return false
	}
	return t.now().Sub(t.since) >= t.timeout
}
