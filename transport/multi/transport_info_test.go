package multi_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/aptpod/iscp-go/transport"
	"github.com/aptpod/iscp-go/transport/multi"
)

// mockMetricsProvider は MetricsProvider インターフェースのモック実装です。
type mockMetricsProvider struct {
	rtt              time.Duration
	rttvar           time.Duration
	congestionWindow uint64
	bytesInFlight    uint64
}

func (m *mockMetricsProvider) RTT() time.Duration {
	return m.rtt
}

func (m *mockMetricsProvider) RTTVar() time.Duration {
	return m.rttvar
}

func (m *mockMetricsProvider) CongestionWindow() uint64 {
	return m.congestionWindow
}

func (m *mockMetricsProvider) BytesInFlight() uint64 {
	return m.bytesInFlight
}

func (m *mockMetricsProvider) Start() error {
	return nil // No-op for mock
}

func (m *mockMetricsProvider) Stop() {
	// No-op for mock
}

func TestPathInfo_NewPathInfo(t *testing.T) {
	transportID := transport.TransportID("test-path")
	provider := &mockMetricsProvider{
		rtt:              50 * time.Millisecond,
		rttvar:           25 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000,
	}

	pathInfo := multi.NewTransportInfo(transportID, provider)

	assert.NotNil(t, pathInfo)
	assert.Equal(t, transportID, pathInfo.TransportID())
}

func TestPathInfo_Update_SendingAllowed(t *testing.T) {
	tests := []struct {
		name             string
		bytesInFlight    uint64
		congestionWindow uint64
		expectedAllowed  bool
	}{
		{
			name:             "success: sending allowed when bytes in flight is less than cwnd",
			bytesInFlight:    5000,
			congestionWindow: 10000,
			expectedAllowed:  true,
		},
		{
			name:             "success: sending not allowed when bytes in flight equals cwnd",
			bytesInFlight:    10000,
			congestionWindow: 10000,
			expectedAllowed:  false,
		},
		{
			name:             "success: sending not allowed when bytes in flight exceeds cwnd",
			bytesInFlight:    15000,
			congestionWindow: 10000,
			expectedAllowed:  false,
		},
		{
			name:             "edge case: sending allowed when both are zero",
			bytesInFlight:    0,
			congestionWindow: 0,
			expectedAllowed:  false,
		},
		{
			name:             "edge case: sending allowed at boundary - 1",
			bytesInFlight:    9999,
			congestionWindow: 10000,
			expectedAllowed:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &mockMetricsProvider{
				bytesInFlight:    tt.bytesInFlight,
				congestionWindow: tt.congestionWindow,
			}

			pathInfo := multi.NewTransportInfo("test", provider)
			pathInfo.Update()

			assert.Equal(t, tt.expectedAllowed, pathInfo.SendingAllowed())
		})
	}
}

func TestPathInfo_Update_NilProvider(t *testing.T) {
	t.Run("success: sending not allowed when provider is nil", func(t *testing.T) {
		pathInfo := multi.NewTransportInfo("test", nil)
		pathInfo.Update()

		assert.False(t, pathInfo.SendingAllowed())
	})
}

func TestPathInfo_MetricsGetters(t *testing.T) {
	provider := &mockMetricsProvider{
		rtt:              75 * time.Millisecond,
		rttvar:           30 * time.Millisecond,
		congestionWindow: 25000,
		bytesInFlight:    8000,
	}

	pathInfo := multi.NewTransportInfo("test", provider)

	tests := []struct {
		name   string
		getter func() any
		want   any
	}{
		{
			name:   "success: SmoothedRTT returns provider RTT",
			getter: func() any { return pathInfo.SmoothedRTT() },
			want:   75 * time.Millisecond,
		},
		{
			name:   "success: MeanDeviation returns provider RTTVar",
			getter: func() any { return pathInfo.MeanDeviation() },
			want:   30 * time.Millisecond,
		},
		{
			name:   "success: CongestionWindow returns provider CWND",
			getter: func() any { return pathInfo.CongestionWindow() },
			want:   uint64(25000),
		},
		{
			name:   "success: BytesInFlight returns provider bytes in flight",
			getter: func() any { return pathInfo.BytesInFlight() },
			want:   uint64(8000),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.getter()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPathInfo_MetricsGetters_NilProvider(t *testing.T) {
	pathInfo := multi.NewTransportInfo("test", nil)

	tests := []struct {
		name   string
		getter func() any
		want   any
	}{
		{
			name:   "success: SmoothedRTT returns default when provider is nil",
			getter: func() any { return pathInfo.SmoothedRTT() },
			want:   100 * time.Millisecond,
		},
		{
			name:   "success: MeanDeviation returns default when provider is nil",
			getter: func() any { return pathInfo.MeanDeviation() },
			want:   50 * time.Millisecond,
		},
		{
			name:   "success: CongestionWindow returns default when provider is nil",
			getter: func() any { return pathInfo.CongestionWindow() },
			want:   uint64(14600),
		},
		{
			name:   "success: BytesInFlight returns zero when provider is nil",
			getter: func() any { return pathInfo.BytesInFlight() },
			want:   uint64(0),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.getter()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPathInfo_PotentiallyFailed(t *testing.T) {
	provider := &mockMetricsProvider{}
	pathInfo := multi.NewTransportInfo("test", provider)

	tests := []struct {
		name      string
		operation func()
		want      bool
	}{
		{
			name:      "success: initial state is not failed",
			operation: func() {},
			want:      false,
		},
		{
			name:      "success: set to failed",
			operation: func() { pathInfo.SetPotentiallyFailed(true) },
			want:      true,
		},
		{
			name:      "success: set back to not failed",
			operation: func() { pathInfo.SetPotentiallyFailed(false) },
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.operation()
			got := pathInfo.PotentiallyFailed()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPathInfo_SendingAllowed_Setter(t *testing.T) {
	provider := &mockMetricsProvider{}
	pathInfo := multi.NewTransportInfo("test", provider)

	tests := []struct {
		name      string
		operation func()
		want      bool
	}{
		{
			name:      "success: initial state is not allowed",
			operation: func() {},
			want:      false,
		},
		{
			name:      "success: set to allowed",
			operation: func() { pathInfo.SetSendingAllowed(true) },
			want:      true,
		},
		{
			name:      "success: set back to not allowed",
			operation: func() { pathInfo.SetSendingAllowed(false) },
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.operation()
			got := pathInfo.SendingAllowed()
			assert.Equal(t, tt.want, got)
		})
	}
}
