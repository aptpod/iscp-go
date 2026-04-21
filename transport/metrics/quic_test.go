package metrics_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/quic-go/quic-go/qlog"
	"github.com/quic-go/quic-go/qlogwriter"
	"github.com/stretchr/testify/assert"

	"github.com/aptpod/iscp-go/v2/transport/metrics"
)

func TestQUICMetricsProvider_Defaults(t *testing.T) {
	provider := metrics.NewQUICMetricsProvider()

	tests := []struct {
		name   string
		getter func() any
		want   any
	}{
		{"success: default RTT is 100ms", func() any { return provider.RTT() }, 100 * time.Millisecond},
		{"success: default RTTVar is 50ms", func() any { return provider.RTTVar() }, 50 * time.Millisecond},
		{"success: default CongestionWindow is 14600 bytes", func() any { return provider.CongestionWindow() }, uint64(14600)},
		{"success: default BytesInFlight is 0", func() any { return provider.BytesInFlight() }, uint64(0)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.getter())
		})
	}
}

func TestQUICMetricsProvider_StartLifecycle(t *testing.T) {
	t.Run("success: start then stop", func(t *testing.T) {
		p := metrics.NewQUICMetricsProvider()
		assert.NoError(t, p.Start())
		p.Stop()
	})

	t.Run("error: start twice", func(t *testing.T) {
		p := metrics.NewQUICMetricsProvider()
		assert.NoError(t, p.Start())
		err := p.Start()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "already started")
	})

	t.Run("error: start after stop", func(t *testing.T) {
		p := metrics.NewQUICMetricsProvider()
		p.Stop()
		err := p.Start()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "already stopped")
	})

	t.Run("success: stop is idempotent", func(t *testing.T) {
		p := metrics.NewQUICMetricsProvider()
		_ = p.Start()
		p.Stop()
		p.Stop()
	})
}

func TestQUICMetricsProvider_UpdateViaRecordEvent(t *testing.T) {
	p := metrics.NewQUICMetricsProvider()
	assert.NoError(t, p.Start())
	defer p.Stop()

	tracerFn := p.Tracer()
	trace := tracerFn(context.Background(), true, qlogwriter.ConnectionID{})
	rec := trace.AddProducer()
	defer rec.Close()

	rec.RecordEvent(qlog.MetricsUpdated{
		SmoothedRTT:      12 * time.Millisecond,
		RTTVariance:      3 * time.Millisecond,
		CongestionWindow: 65536,
		BytesInFlight:    8192,
	})

	assert.Equal(t, 12*time.Millisecond, p.RTT())
	assert.Equal(t, 3*time.Millisecond, p.RTTVar())
	assert.Equal(t, uint64(65536), p.CongestionWindow())
	assert.Equal(t, uint64(8192), p.BytesInFlight())
}

func TestQUICMetricsProvider_RecordEventValueAndPointer(t *testing.T) {
	tests := []struct {
		name string
		ev   qlogwriter.Event
	}{
		{"value type", qlog.MetricsUpdated{SmoothedRTT: 20 * time.Millisecond, CongestionWindow: 1000}},
		{"pointer type", &qlog.MetricsUpdated{SmoothedRTT: 20 * time.Millisecond, CongestionWindow: 1000}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := metrics.NewQUICMetricsProvider()
			assert.NoError(t, p.Start())
			defer p.Stop()

			rec := p.Tracer()(context.Background(), true, qlogwriter.ConnectionID{}).AddProducer()
			defer rec.Close()

			rec.RecordEvent(tt.ev)

			assert.Equal(t, 20*time.Millisecond, p.RTT())
			assert.Equal(t, uint64(1000), p.CongestionWindow())
		})
	}
}

func TestQUICMetricsProvider_IgnoresOtherEvents(t *testing.T) {
	p := metrics.NewQUICMetricsProvider()
	assert.NoError(t, p.Start())
	defer p.Stop()

	rec := p.Tracer()(context.Background(), true, qlogwriter.ConnectionID{}).AddProducer()
	defer rec.Close()

	rec.RecordEvent(qlog.MTUUpdated{Value: 1200, Done: true})

	assert.Equal(t, 100*time.Millisecond, p.RTT())
	assert.Equal(t, uint64(0), p.BytesInFlight())
}

func TestQUICMetricsProvider_SupportsSchemas(t *testing.T) {
	p := metrics.NewQUICMetricsProvider()
	trace := p.Tracer()(context.Background(), true, qlogwriter.ConnectionID{})

	assert.True(t, trace.SupportsSchemas(qlog.EventSchema))
	assert.False(t, trace.SupportsSchemas("urn:ietf:params:qlog:events:unknown"))
}

func TestQUICMetricsProvider_RecorderCloseClears(t *testing.T) {
	p := metrics.NewQUICMetricsProvider()
	assert.NoError(t, p.Start())
	defer p.Stop()

	rec := p.Tracer()(context.Background(), true, qlogwriter.ConnectionID{}).AddProducer()
	rec.RecordEvent(qlog.MetricsUpdated{
		SmoothedRTT:      12 * time.Millisecond,
		RTTVariance:      3 * time.Millisecond,
		CongestionWindow: 65536,
		BytesInFlight:    8192,
	})

	assert.Equal(t, 12*time.Millisecond, p.RTT())
	assert.Equal(t, uint64(8192), p.BytesInFlight())

	assert.NoError(t, rec.Close())

	// Close 後: デフォルト値に戻る (TCP 実装と同一挙動)
	assert.Equal(t, 100*time.Millisecond, p.RTT())
	assert.Equal(t, 50*time.Millisecond, p.RTTVar())
	assert.Equal(t, uint64(14600), p.CongestionWindow())
	assert.Equal(t, uint64(0), p.BytesInFlight())
}

func TestQUICMetricsProvider_StopIgnoresRecordEvent(t *testing.T) {
	p := metrics.NewQUICMetricsProvider()
	assert.NoError(t, p.Start())

	rec := p.Tracer()(context.Background(), true, qlogwriter.ConnectionID{}).AddProducer()
	defer rec.Close()

	p.Stop()
	rec.RecordEvent(qlog.MetricsUpdated{
		SmoothedRTT:      99 * time.Millisecond,
		CongestionWindow: 99999,
	})

	assert.Equal(t, 100*time.Millisecond, p.RTT())
	assert.Equal(t, uint64(14600), p.CongestionWindow())
}

func TestQUICMetricsProvider_Concurrent(t *testing.T) {
	p := metrics.NewQUICMetricsProvider()
	assert.NoError(t, p.Start())
	defer p.Stop()

	rec := p.Tracer()(context.Background(), true, qlogwriter.ConnectionID{}).AddProducer()
	defer rec.Close()

	var wg sync.WaitGroup
	numGoroutines := 10
	iterations := 100

	t.Run("success: concurrent RecordEvent and getter without races", func(t *testing.T) {
		for i := range numGoroutines {
			wg.Add(1)
			go func(seed int) {
				defer wg.Done()
				for j := range iterations {
					rec.RecordEvent(qlog.MetricsUpdated{
						SmoothedRTT:      time.Duration(seed+j+1) * time.Millisecond,
						RTTVariance:      time.Duration(seed+1) * time.Millisecond,
						CongestionWindow: (seed + 1) * 1024,
						BytesInFlight:    (j + 1) * 128,
					})
				}
			}(i)
		}
		for range numGoroutines {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range iterations {
					_ = p.RTT()
					_ = p.RTTVar()
					_ = p.CongestionWindow()
					_ = p.BytesInFlight()
				}
			}()
		}
		wg.Wait()
	})
}
