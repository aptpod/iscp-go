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

// dialQUICForMinRTT は、共有の QUIC エコーサーバーへ実接続を張り、
// その QUICMetricsProvider の実インスタンス (transport.MetricsSupporter 経由) を返します。
// ecf_integration_quic_test.go の startQUICEchoServer / newQUICDialer を再利用します。
// reconnect.Transport でラップせず生の QUIC トランスポートを使うのは、
// QUICMetricsProvider の実インスタンスへ直接 RecordEvent で RTT を注入するため
// (reconnect.Transport は metricsProvider を内部で保持するのみで外部に公開しない)。
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

// recordQUICRTT は、QUICMetricsProvider の Tracer/Recorder 経由で
// qlog.MetricsUpdated イベントを push し、SmoothedRTT (延いては MinRTT) を人工的に設定します。
// transport/metrics/quic_test.go の TestQUICMetricsProvider_UpdateViaRecordEvent と同じ push 手法です。
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

// TestMinRTTSelectorIntegration_QUIC_SelectsMinimumRTT は、MinRTTSelector に
// QUICMetricsProvider 経由で RTT を更新すると最小 RTT のトランスポートが
// 選択されることを確認します（IC2-10028 受け入れ基準）。
//
// 実 QUIC 接続を張ると、quic-go 自身がハンドシェイクの ACK 処理から非同期に
// qlog.MetricsUpdated イベントを push し続けるため、ここで注入する人工的な RTT
// と競合し、ローカルループバックの実測 RTT（サブ ms）付近に両者の minRTT が
// 収束してテストが不安定になる（MinRTTSelector は最小値を保持し続ける設計）。
// 実装が transport.MetricsSupporter を正しく実装しているかの確認は
// TestMinRTTSelectorIntegration_QUIC_MetricsUpdateLoop / _DefaultMetricsNoCrash
// で実接続を使ってカバー済みのため、ここでは QUICMetricsProvider を直接生成し
// RecordEvent で決定論的に RTT を注入する。
func TestMinRTTSelectorIntegration_QUIC_SelectsMinimumRTT(t *testing.T) {
	providerFast := metrics.NewQUICMetricsProvider()
	providerSlow := metrics.NewQUICMetricsProvider()

	// QUICMetricsProvider へ RTT を push（quic-go の qlog.MetricsUpdated イベント経路を模擬）。
	recordQUICRTT(providerFast, 20*time.Millisecond)
	recordQUICRTT(providerSlow, 100*time.Millisecond)

	selector := NewMinRTTSelector()
	selector.UpdateTransport("fast", NewTransportInfo("fast", providerFast))
	selector.UpdateTransport("slow", NewTransportInfo("slow", providerSlow))

	selectedID := selector.Get(context.Background(), 1000)
	assert.Equal(t, transport.SubConnectionID("fast"), selectedID)

	// MinRTT はベースRTT（観測された最小値）を追跡するため、fast 側に既存の
	// minRTT(20ms) より大きい RTT(150ms) を push しても選択には影響しない一方、
	// slow 側に既存の minRTT(100ms) より小さい RTT(10ms) を push すると
	// slow の MinRTT が更新され、経路選択が切り替わることを確認する。
	recordQUICRTT(providerFast, 150*time.Millisecond)
	recordQUICRTT(providerSlow, 10*time.Millisecond)
	selector.UpdateTransport("fast", NewTransportInfo("fast", providerFast))
	selector.UpdateTransport("slow", NewTransportInfo("slow", providerSlow))

	selectedID = selector.Get(context.Background(), 1000)
	assert.Equal(t, transport.SubConnectionID("slow"), selectedID)
}

// TestMinRTTSelectorIntegration_QUIC_MetricsUpdateLoop は、
// TestECFSelectorIntegration_QUIC_MetricsUpdateLoop と同じセットアップ手法で、
// MinRTTSelector を組み込んだ multi.Transport がメトリクス更新ループ稼働中も
// QUIC 経由でクラッシュせず、トランスポートを選択できることを確認します。
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

	// メトリクス更新ループが動作するのを待機（デフォルト値のまま更新され続けてもクラッシュしないことを確認）。
	time.Sleep(150 * time.Millisecond)

	selectedID := selector.Get(context.Background(), 1000)
	assert.Equal(t, transport.SubConnectionID("quic1"), selectedID)
}

// TestMinRTTSelectorIntegration_QUIC_DefaultMetricsNoCrash は、複数の QUIC 接続を
// メトリクス未更新（QUICMetricsProvider のデフォルト値: RTT=100ms/RTTVar=50ms/CWND=14600B）
// のまま MinRTTSelector に登録してもクラッシュせず、いずれかのトランスポートが
// 安定して選択され続けることを確認します（IC2-10028 受け入れ基準）。
func TestMinRTTSelectorIntegration_QUIC_DefaultMetricsNoCrash(t *testing.T) {
	addr, closeFn := startQUICEchoServer(t)
	t.Cleanup(closeFn)

	providerA := dialQUICForMinRTT(t, addr)
	providerB := dialQUICForMinRTT(t, addr)

	selector := NewMinRTTSelector()
	// RecordEvent を一度も呼ばず、QUICMetricsProvider のデフォルト値のまま登録する。
	selector.UpdateTransport("a", NewTransportInfo("a", providerA))
	selector.UpdateTransport("b", NewTransportInfo("b", providerB))

	require.NotPanics(t, func() {
		for range 10 {
			selectedID := selector.Get(context.Background(), 1000)
			assert.Contains(t, []transport.SubConnectionID{"a", "b"}, selectedID)
		}
	})
}
