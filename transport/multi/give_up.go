package multi

import (
	"sync"
	"time"
)

// reconnect.Dial の defaults の複製。
//
// reconnect パッケージは本設計で変更しない方針のため公開定数を追加できず、
// ここで値を複製している。参照元は transport/reconnect/transport.go:138-143。
// ズレは give_up_defaults_test.go の
// TestCalcNoConnectedTransportTimeout_MatchesReconnectDefaults が検知する。
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

// noConnectedTracker は「接続済みの sub-connection が 1 本も無い状態」の継続時間を
// 追跡し、閾値を超えたことを検出します。
//
// 実装上の要件（spec 設計 3）:
//   - level-trigger: 呼び出しごとに「今の観測」から判定する。状態遷移イベントに
//     依存すると、誤クリア後に次の遷移が来ない無音障害を取り逃す
//   - 単一ロック下の check-and-set: 観測の読み取りと記録の書き込みを分けると、
//     複数の status callback と ticker から並行に呼ばれたとき逆順 interleave で
//     誤クリア・誤残留が起きる
//   - monotonic clock: time.Now() / time.Since() を使う。Unix 時刻の比較は
//     時計の巻き戻しで誤判定する
type noConnectedTracker struct {
	mu      sync.Mutex
	timeout time.Duration
	// since は「接続済みが 1 本も無い状態」になった時刻。ゼロ値は未記録を表す。
	since time.Time
	// now はテストから差し替えるための時刻取得関数。
	now func() time.Time
}

// newNoConnectedTracker は追跡器を作ります。timeout が 0 以下のとき、
// observe は常に false を返します（＝畳まない）。
func newNoConnectedTracker(timeout time.Duration) *noConnectedTracker {
	return &noConnectedTracker{
		timeout: timeout,
		now:     time.Now,
	}
}

// observe は現時点の観測を記録し、「接続済みが 1 本も無い状態」が閾値を超えて
// 継続しているなら true を返します。
//
// hasConnected は「接続済み（StatusConnected）の sub-connection が 1 本以上あるか」です。
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
