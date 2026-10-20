package multi_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/aptpod/iscp-go/v2/transport"
	"github.com/aptpod/iscp-go/v2/transport/multi"
)

// mockMetricsProvider is a mock implementation of the MetricsProvider interface.
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
	transportID := transport.SubConnectionID("test-path")
	provider := &mockMetricsProvider{
		rtt:              50 * time.Millisecond,
		rttvar:           25 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000,
	}

	pathInfo := multi.NewTransportInfo(transportID, provider)

	assert.NotNil(t, pathInfo)
	assert.Equal(t, transportID, pathInfo.SubConnectionID())
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

func TestTransportInfo_SetMetricsProvider(t *testing.T) {
	t.Run("success: Update uses new provider after SetMetricsProvider", func(t *testing.T) {
		provider1 := &mockMetricsProvider{
			rtt:              50 * time.Millisecond,
			rttvar:           25 * time.Millisecond,
			congestionWindow: 20000,
			bytesInFlight:    5000,
		}
		info := multi.NewTransportInfo("test", provider1)
		info.Update()

		// Verify that the metrics of provider1 are reflected
		assert.Equal(t, 50*time.Millisecond, info.SmoothedRTT())
		assert.Equal(t, 25*time.Millisecond, info.MeanDeviation())
		assert.True(t, info.SendingAllowed())

		// Swap the provider
		provider2 := &mockMetricsProvider{
			rtt:              100 * time.Millisecond,
			rttvar:           40 * time.Millisecond,
			congestionWindow: 30000,
			bytesInFlight:    30000,
		}
		info.SetMetricsProvider(provider2)
		info.Update()

		// Verify that the metrics of provider2 are reflected
		assert.Equal(t, 100*time.Millisecond, info.SmoothedRTT())
		assert.Equal(t, 40*time.Millisecond, info.MeanDeviation())
		assert.Equal(t, uint64(30000), info.CongestionWindow())
		assert.Equal(t, uint64(30000), info.BytesInFlight())
		assert.False(t, info.SendingAllowed())
	})

	t.Run("success: minRTT is preserved after SetMetricsProvider", func(t *testing.T) {
		provider1 := &mockMetricsProvider{
			rtt:              50 * time.Millisecond,
			congestionWindow: 20000,
			bytesInFlight:    5000,
		}
		info := multi.NewTransportInfo("test", provider1)
		info.Update()

		// Verify that minRTT is set to 50ms
		assert.Equal(t, 50*time.Millisecond, info.MinRTT())

		// Swap the provider (with a larger RTT)
		provider2 := &mockMetricsProvider{
			rtt:              100 * time.Millisecond,
			congestionWindow: 20000,
			bytesInFlight:    5000,
		}
		info.SetMetricsProvider(provider2)
		info.Update()

		// minRTT stays at 50ms (it must not be updated to 100ms)
		assert.Equal(t, 50*time.Millisecond, info.MinRTT())
	})
}
