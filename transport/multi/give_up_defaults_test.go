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
			name:     "negative value means unlimited, so do not give up",
			attempts: -1,
			interval: time.Second,
			want:     0,
		},
		{
			name:     "-2 is also negative, so do not give up",
			attempts: -2,
			interval: time.Second,
			want:     0,
		},
		{
			name:     "0 means unset, so apply the reconnect.Dial default attempts of 30",
			attempts: 0,
			interval: 2 * time.Second,
			want:     60 * time.Second,
		},
		{
			name:     "a positive finite value is used as the attempts as is",
			attempts: 5,
			interval: 3 * time.Second,
			want:     15 * time.Second,
		},
		{
			name:     "when interval is 0, apply the reconnect.Dial default of 1 second",
			attempts: 4,
			interval: 0,
			want:     4 * time.Second,
		},
		{
			name:     "when interval is negative, also apply the default of 1 second",
			attempts: 4,
			interval: -time.Second,
			want:     4 * time.Second,
		},
		{
			name:     "both unset results in 30 seconds",
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

// TestCalcNoConnectedTransportTimeout_MatchesReconnectDefaults pins that
// the defaults duplicated in give_up.go match the defaults of reconnect.Dial.
// The reconnect package is deliberately left unchanged, so the constants cannot be exported and
// the values are duplicated on the multi side. This test fails if they drift apart.
func TestCalcNoConnectedTransportTimeout_MatchesReconnectDefaults(t *testing.T) {
	// When Dial is called with MaxReconnectAttempts / ReconnectInterval unset,
	// reconnect.Dial applies the defaults and puts them in the negotiation parameters.
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
		// MaxReconnectAttempts and ReconnectInterval are intentionally left unset.
		HeartbeatInterval: time.Hour,
		HeartbeatTimeout:  time.Hour,
		Logger:            log.NewNop(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })

	got := rt.NegotiationParams().MaxReconnectAttempts
	require.NotNil(t, got, "reconnect.Dial should put MaxReconnectAttempts in the negotiation parameters")

	// If the default attempts change, the result of CalcNoConnectedTransportTimeout(0, interval) changes.
	require.Equal(t,
		time.Duration(*got)*time.Second,
		CalcNoConnectedTransportTimeout(0, time.Second),
		"the default attempts on the multi side differ from the defaults of reconnect.Dial. Update defaultMaxReconnectAttempts in give_up.go")
}
