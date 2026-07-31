package multi_test

import (
	"testing"
	"time"

	"github.com/aptpod/iscp-go/v2/log"
	"github.com/aptpod/iscp-go/v2/transport"
	. "github.com/aptpod/iscp-go/v2/transport/multi"
	"github.com/aptpod/iscp-go/v2/transport/reconnect"
	"github.com/stretchr/testify/require"
)

func TestCalcNoConnectedTransportTimeout(t *testing.T) {
	tests := []struct {
		name     string
		attempts int
		interval time.Duration
		want     time.Duration
	}{
		{
			name:     "負値は無期限なので畳まない",
			attempts: -1,
			interval: time.Second,
			want:     0,
		},
		{
			name:     "-2 も負値なので畳まない",
			attempts: -2,
			interval: time.Second,
			want:     0,
		},
		{
			name:     "0 は未設定なので reconnect.Dial の既定回数 30 を適用する",
			attempts: 0,
			interval: 2 * time.Second,
			want:     60 * time.Second,
		},
		{
			name:     "正の有限値はそのまま回数として使う",
			attempts: 5,
			interval: 3 * time.Second,
			want:     15 * time.Second,
		},
		{
			name:     "interval が 0 のときは reconnect.Dial の既定値 1 秒を適用する",
			attempts: 4,
			interval: 0,
			want:     4 * time.Second,
		},
		{
			name:     "interval が負のときも既定値 1 秒を適用する",
			attempts: 4,
			interval: -time.Second,
			want:     4 * time.Second,
		},
		{
			name:     "未設定同士の組み合わせは 30 秒になる",
			attempts: 0,
			interval: 0,
			want:     30 * time.Second,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, CalcNoConnectedTransportTimeout(tt.attempts, tt.interval))
		})
	}
}

// TestCalcNoConnectedTransportTimeout_MatchesReconnectDefaults は、
// give_up.go が持つ既定値の複製が reconnect.Dial の defaults と一致していることを固定する。
// reconnect パッケージは本 spec で変更しない方針のため定数を公開できず、multi 側で
// 値を複製している。ズレたらこのテストが落ちる。
func TestCalcNoConnectedTransportTimeout_MatchesReconnectDefaults(t *testing.T) {
	// MaxReconnectAttempts / ReconnectInterval を未設定のまま Dial すると、
	// reconnect.Dial が defaults を適用してネゴシエーションパラメータに載せる。
	mock := newMockTransport("defaults-probe")
	rt, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			return mock, nil
		}),
		DialConfig: transport.DialConfig{
			SubConnectionID:   "defaults-probe",
			SuperConnectionID: transport.SuperConnectionID(testSuperConnectionID),
			TransportType:     transport.NegotiationNameWebSocket,
		},
		// MaxReconnectAttempts と ReconnectInterval は意図的に未設定にする。
		HeartbeatInterval: time.Hour,
		HeartbeatTimeout:  time.Hour,
		Logger:            log.NewNop(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })

	got := rt.NegotiationParams().MaxReconnectAttempts
	require.NotNil(t, got, "reconnect.Dial はネゴシエーションパラメータに MaxReconnectAttempts を載せるはず")

	// 既定回数が変わったら CalcNoConnectedTransportTimeout(0, interval) の結果が変わる。
	require.Equal(t,
		time.Duration(*got)*time.Second,
		CalcNoConnectedTransportTimeout(0, time.Second),
		"multi 側の既定回数が reconnect.Dial の defaults とズレている。give_up.go の defaultMaxReconnectAttempts を更新すること")
}
