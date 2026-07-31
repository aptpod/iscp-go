package multi

import "time"

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
