package multi_test

import (
	"context"
	"testing"
	"time"

	"github.com/quic-go/quic-go/qlog"
	"github.com/quic-go/quic-go/qlogwriter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aptpod/iscp-go/v2/log"
	"github.com/aptpod/iscp-go/v2/transport"
	"github.com/aptpod/iscp-go/v2/transport/compress"
	"github.com/aptpod/iscp-go/v2/transport/metrics"
	. "github.com/aptpod/iscp-go/v2/transport/multi"
	"github.com/aptpod/iscp-go/v2/transport/reconnect"
)

// dialQUICForMinRTT makes a real connection to the shared QUIC echo server and
// returns the real QUICMetricsProvider instance (via transport.MetricsSupporter).
// It reuses startQUICEchoServer / newQUICDialer from ecf_integration_quic_test.go.
// A raw QUIC transport is used without wrapping it in reconnect.Transport
// because RTT is injected directly into the real QUICMetricsProvider instance with RecordEvent
// (reconnect.Transport only holds metricsProvider internally and does not expose it).
func dialQUICForMinRTT(t testing.TB, addr string) *metrics.QUICMetricsProvider {
	t.Helper()

	tr, err := newQUICDialer().Dial(transport.DialConfig{
		Address:           addr,
		CompressConfig:    compress.Config{},
		EncodingName:      transport.EncodingNameJSON,
		SuperConnectionID: "test-group",
	})
	require.NoError(t, err)
	t.Cleanup(func() { tr.Close() })

	ms, ok := tr.(transport.MetricsSupporter)
	require.True(t, ok, "QUIC transport should implement transport.MetricsSupporter")
	provider, ok := ms.MetricsProvider().(*metrics.QUICMetricsProvider)
	require.True(t, ok, "MetricsProvider should be *metrics.QUICMetricsProvider")

	return provider
}

// recordQUICRTT pushes a qlog.MetricsUpdated event through the Tracer/Recorder
// of QUICMetricsProvider to set SmoothedRTT (and therefore MinRTT) artificially.
// It uses the same push technique as TestQUICMetricsProvider_UpdateViaRecordEvent in transport/metrics/quic_test.go.
func recordQUICRTT(provider *metrics.QUICMetricsProvider, rtt time.Duration) {
	trace := provider.Tracer()(context.Background(), true, qlogwriter.ConnectionID{})
	rec := trace.AddProducer()
	rec.RecordEvent(qlog.MetricsUpdated{
		SmoothedRTT:      rtt,
		RTTVariance:      rtt / 2,
		CongestionWindow: 65536,
		BytesInFlight:    1024,
	})
}

// TestMinRTTSelectorIntegration_QUIC_SelectsMinimumRTT verifies that when the RTT is updated
// through QUICMetricsProvider, MinRTTSelector selects the transport with the
// minimum RTT.
//
// With a real QUIC connection, quic-go itself keeps pushing qlog.MetricsUpdated events
// asynchronously from its handshake ACK processing, which conflicts with the artificial RTT injected here.
// Both minRTT values then converge near the measured local loopback RTT (sub-ms)
// and the test becomes unstable (MinRTTSelector is designed to keep the minimum value).
// Whether the implementation correctly implements transport.MetricsSupporter is covered by
// TestMinRTTSelectorIntegration_QUIC_MetricsUpdateLoop / _DefaultMetricsNoCrash
// with real connections, so here QUICMetricsProvider is created directly and
// the RTT is injected deterministically with RecordEvent.
func TestMinRTTSelectorIntegration_QUIC_SelectsMinimumRTT(t *testing.T) {
	providerFast := metrics.NewQUICMetricsProvider()
	providerSlow := metrics.NewQUICMetricsProvider()

	// Push the RTT to QUICMetricsProvider (simulating the quic-go qlog.MetricsUpdated event path).
	recordQUICRTT(providerFast, 20*time.Millisecond)
	recordQUICRTT(providerSlow, 100*time.Millisecond)

	selector := NewMinRTTSelector()
	selector.UpdateTransport("fast", NewTransportInfo("fast", providerFast))
	selector.UpdateTransport("slow", NewTransportInfo("slow", providerSlow))

	selectedID := selector.Get(context.Background(), 1000)
	assert.Equal(t, transport.SubConnectionID("fast"), selectedID)

	// MinRTT tracks the base RTT (the minimum observed value). So pushing an RTT (150ms) larger than the existing
	// minRTT (20ms) to the fast side does not affect selection, while pushing an RTT (10ms)
	// smaller than the existing minRTT (100ms) to the slow side
	// updates the MinRTT of slow and switches the path selection. This is what we verify.
	recordQUICRTT(providerFast, 150*time.Millisecond)
	recordQUICRTT(providerSlow, 10*time.Millisecond)
	selector.UpdateTransport("fast", NewTransportInfo("fast", providerFast))
	selector.UpdateTransport("slow", NewTransportInfo("slow", providerSlow))

	selectedID = selector.Get(context.Background(), 1000)
	assert.Equal(t, transport.SubConnectionID("slow"), selectedID)
}

// TestMinRTTSelectorIntegration_QUIC_MetricsUpdateLoop uses the same setup technique as
// TestECFSelectorIntegration_QUIC_MetricsUpdateLoop to verify that
// a multi.Transport with MinRTTSelector does not crash and can select a transport
// over QUIC while the metrics update loop is running.
func TestMinRTTSelectorIntegration_QUIC_MetricsUpdateLoop(t *testing.T) {
	addr, closeFn := startQUICEchoServer(t)
	t.Cleanup(closeFn)

	tr, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: newQUICDialer(),
		DialConfig: transport.DialConfig{
			Address:           addr,
			CompressConfig:    compress.Config{},
			EncodingName:      transport.EncodingNameJSON,
			SuperConnectionID: "test-group",
		},
		MaxReconnectAttempts: 10,
		ReconnectInterval:    time.Millisecond * 100,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { tr.Close() })

	require.Eventually(t,
		func() bool { return tr.Status() == reconnect.StatusConnected },
		time.Second, 10*time.Millisecond,
		"status should become Connected",
	)

	selector := NewMinRTTSelector()
	mt, err := NewTransport(TransportConfig{
		TransportMap: TransportMap{
			"quic1": tr,
		},
		TransportSelector: selector,
		Logger:            log.NewNop(),
	})
	require.NoError(t, err)
	defer mt.Close()

	// Wait for the metrics update loop to run (verify that it does not crash even if updates continue with default values).
	time.Sleep(150 * time.Millisecond)

	selectedID := selector.Get(context.Background(), 1000)
	assert.Equal(t, transport.SubConnectionID("quic1"), selectedID)
}

// TestMinRTTSelectorIntegration_QUIC_DefaultMetricsNoCrash verifies that registering multiple QUIC connections
// with MinRTTSelector with metrics never updated (QUICMetricsProvider defaults: RTT=100ms/RTTVar=50ms/CWND=14600B)
// does not crash and that some transport
// keeps being selected stably.
func TestMinRTTSelectorIntegration_QUIC_DefaultMetricsNoCrash(t *testing.T) {
	addr, closeFn := startQUICEchoServer(t)
	t.Cleanup(closeFn)

	providerA := dialQUICForMinRTT(t, addr)
	providerB := dialQUICForMinRTT(t, addr)

	selector := NewMinRTTSelector()
	// Register with the QUICMetricsProvider defaults, without ever calling RecordEvent.
	selector.UpdateTransport("a", NewTransportInfo("a", providerA))
	selector.UpdateTransport("b", NewTransportInfo("b", providerB))

	require.NotPanics(t, func() {
		for range 10 {
			selectedID := selector.Get(context.Background(), 1000)
			assert.Contains(t, []transport.SubConnectionID{"a", "b"}, selectedID)
		}
	})
}
