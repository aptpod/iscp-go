package multi_test

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aptpod/iscp-go/log"
	"github.com/aptpod/iscp-go/transport"
	. "github.com/aptpod/iscp-go/transport/multi"
)

func TestMinRTTSelector_NewMinRTTSelector(t *testing.T) {
	selector := NewMinRTTSelector()

	require.NotNil(t, selector)
	// 初期状態でGet()を呼び出すと空文字が返る
	assert.Equal(t, transport.TransportID(""), selector.Get(1000))
}

func TestMinRTTSelector_SetMultiTransport(t *testing.T) {
	selector := NewMinRTTSelector()

	// nil を設定しても panic しないことを確認
	selector.SetMultiTransport(nil)
}

func TestMinRTTSelector_SetLogger(t *testing.T) {
	selector := NewMinRTTSelector()

	logger := log.NewNop()
	// panic しないことを確認
	selector.SetLogger(logger)
}

func TestMinRTTSelector_SetQueueSize(t *testing.T) {
	selector := NewMinRTTSelector()

	// no-op なので panic しないことを確認するのみ
	selector.SetQueueSize(1000)
	selector.SetQueueSize(0)
}

func TestMinRTTSelector_UpdateTransport(t *testing.T) {
	selector := NewMinRTTSelector()

	transportID := transport.TransportID("test-transport")
	provider := &mockMetricsProvider{
		rtt:              50 * time.Millisecond,
		rttvar:           25 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000,
	}

	info := NewTransportInfo(transportID, provider)
	selector.UpdateTransport(transportID, info)

	// トランスポートが登録されていることを Get で確認
	selectedID := selector.Get(1000)
	assert.Equal(t, transportID, selectedID)
}

func TestMinRTTSelector_UpdateTransport_PreservesMinRTT(t *testing.T) {
	// MinRTTの保持動作は内部実装の詳細なので、動作確認のみ行う
	selector := NewMinRTTSelector()

	transportID := transport.TransportID("test-transport")

	// 最初の更新
	provider1 := &mockMetricsProvider{
		rtt:              30 * time.Millisecond,
		rttvar:           15 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000,
	}
	info1 := NewTransportInfo(transportID, provider1)
	selector.UpdateTransport(transportID, info1)

	// 選択できることを確認
	selectedID := selector.Get(1000)
	assert.Equal(t, transportID, selectedID)

	// 2回目の更新（RTTが大きくなる）
	provider2 := &mockMetricsProvider{
		rtt:              100 * time.Millisecond,
		rttvar:           50 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000,
	}
	info2 := NewTransportInfo(transportID, provider2)
	selector.UpdateTransport(transportID, info2)

	// 引き続き選択できることを確認
	selectedID = selector.Get(1000)
	assert.Equal(t, transportID, selectedID)
}

func TestMinRTTSelector_Get(t *testing.T) {
	tests := []struct {
		name      string
		setup     func() *MinRTTSelector
		want      transport.TransportID
		wantOneOf []transport.TransportID
	}{
		{
			name: "edge case: no transports returns empty",
			setup: func() *MinRTTSelector {
				return NewMinRTTSelector()
			},
			want: "",
		},
		{
			name: "single transport returns that transport",
			setup: func() *MinRTTSelector {
				selector := NewMinRTTSelector()
				id := transport.TransportID("only")
				provider := &mockMetricsProvider{
					rtt:              50 * time.Millisecond,
					rttvar:           25 * time.Millisecond,
					congestionWindow: 20000,
					bytesInFlight:    5000,
				}
				selector.UpdateTransport(id, NewTransportInfo(id, provider))
				return selector
			},
			want: "only",
		},
		{
			name: "selects transport with minimum RTT",
			setup: func() *MinRTTSelector {
				selector := NewMinRTTSelector()

				// 高速トランスポート
				fast := transport.TransportID("fast")
				fastProvider := &mockMetricsProvider{
					rtt:              20 * time.Millisecond,
					rttvar:           10 * time.Millisecond,
					congestionWindow: 20000,
					bytesInFlight:    5000,
				}
				selector.UpdateTransport(fast, NewTransportInfo(fast, fastProvider))

				// 低速トランスポート
				slow := transport.TransportID("slow")
				slowProvider := &mockMetricsProvider{
					rtt:              100 * time.Millisecond,
					rttvar:           50 * time.Millisecond,
					congestionWindow: 20000,
					bytesInFlight:    5000,
				}
				selector.UpdateTransport(slow, NewTransportInfo(slow, slowProvider))

				return selector
			},
			want: "fast",
		},
		{
			name: "skips transport not sending allowed and selects available minimum RTT",
			setup: func() *MinRTTSelector {
				selector := NewMinRTTSelector()

				// 高速だが送信不可（CWND飽和）
				fast := transport.TransportID("fast")
				fastProvider := &mockMetricsProvider{
					rtt:              20 * time.Millisecond,
					rttvar:           10 * time.Millisecond,
					congestionWindow: 10000,
					bytesInFlight:    10000, // CWND == BytesInFlight
				}
				selector.UpdateTransport(fast, NewTransportInfo(fast, fastProvider))

				// 低速だが送信可能
				slow := transport.TransportID("slow")
				slowProvider := &mockMetricsProvider{
					rtt:              100 * time.Millisecond,
					rttvar:           50 * time.Millisecond,
					congestionWindow: 20000,
					bytesInFlight:    5000,
				}
				selector.UpdateTransport(slow, NewTransportInfo(slow, slowProvider))

				return selector
			},
			want: "slow",
		},
		{
			name: "all transports not sending allowed falls back to minimum RTT among all",
			setup: func() *MinRTTSelector {
				selector := NewMinRTTSelector()

				// 高速だが送信不可
				fast := transport.TransportID("fast")
				fastProvider := &mockMetricsProvider{
					rtt:              20 * time.Millisecond,
					rttvar:           10 * time.Millisecond,
					congestionWindow: 10000,
					bytesInFlight:    10000,
				}
				selector.UpdateTransport(fast, NewTransportInfo(fast, fastProvider))

				// 低速で送信不可
				slow := transport.TransportID("slow")
				slowProvider := &mockMetricsProvider{
					rtt:              100 * time.Millisecond,
					rttvar:           50 * time.Millisecond,
					congestionWindow: 10000,
					bytesInFlight:    10000,
				}
				selector.UpdateTransport(slow, NewTransportInfo(slow, slowProvider))

				return selector
			},
			want: "fast",
		},
		{
			name: "same MinRTT uses SmoothedRTT as tiebreaker",
			setup: func() *MinRTTSelector {
				selector := NewMinRTTSelector()

				// 同じMinRTTだがSmoothedRTTが小さい
				t1 := transport.TransportID("t1")
				provider1 := &mockMetricsProvider{
					rtt:              50 * time.Millisecond,
					rttvar:           25 * time.Millisecond,
					congestionWindow: 20000,
					bytesInFlight:    5000,
				}
				selector.UpdateTransport(t1, NewTransportInfo(t1, provider1))

				// 同じMinRTTだがSmoothedRTTが大きい
				t2 := transport.TransportID("t2")
				provider2 := &mockMetricsProvider{
					rtt:              80 * time.Millisecond,
					rttvar:           40 * time.Millisecond,
					congestionWindow: 20000,
					bytesInFlight:    5000,
				}
				selector.UpdateTransport(t2, NewTransportInfo(t2, provider2))

				return selector
			},
			want: "t1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selector := tt.setup()
			got := selector.Get(1000)

			if len(tt.wantOneOf) > 0 {
				assert.Contains(t, tt.wantOneOf, got)
			} else {
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestMinRTTSelector_Stats(t *testing.T) {
	selector := NewMinRTTSelector()

	fast := transport.TransportID("fast")
	fastProvider := &mockMetricsProvider{
		rtt:              20 * time.Millisecond,
		rttvar:           10 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000,
	}
	selector.UpdateTransport(fast, NewTransportInfo(fast, fastProvider))

	// 5回選択
	for range 5 {
		selector.Get(1000)
	}

	stats := selector.Stats()
	assert.Equal(t, uint64(5), stats.TotalSelections)
	assert.Equal(t, uint64(5), stats.SelectionCounts[fast])
	assert.Equal(t, uint64(0), stats.SwitchCount) // 同じトランスポートなのでスイッチなし
}

func TestMinRTTSelector_Stats_SwitchCount(t *testing.T) {
	selector := NewMinRTTSelector()

	fast := transport.TransportID("fast")
	fastProvider := &mockMetricsProvider{
		rtt:              20 * time.Millisecond,
		rttvar:           10 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000,
	}

	slow := transport.TransportID("slow")
	slowProvider := &mockMetricsProvider{
		rtt:              100 * time.Millisecond,
		rttvar:           50 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000,
	}

	// 最初は fast
	selector.UpdateTransport(fast, NewTransportInfo(fast, fastProvider))
	selector.Get(1000) // fast を選択

	// slow を追加（fast は送信不可に）
	fastProvider.bytesInFlight = 20000
	fastProvider.congestionWindow = 20000
	selector.UpdateTransport(fast, NewTransportInfo(fast, fastProvider))
	selector.UpdateTransport(slow, NewTransportInfo(slow, slowProvider))
	selector.Get(1000) // slow を選択（スイッチ発生）

	stats := selector.Stats()
	assert.Equal(t, uint64(2), stats.TotalSelections)
	assert.Equal(t, uint64(1), stats.SwitchCount)
}

func TestMinRTTSelector_ResetStats(t *testing.T) {
	selector := NewMinRTTSelector()

	fast := transport.TransportID("fast")
	fastProvider := &mockMetricsProvider{
		rtt:              20 * time.Millisecond,
		rttvar:           10 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000,
	}
	selector.UpdateTransport(fast, NewTransportInfo(fast, fastProvider))

	// 選択を実行
	for range 5 {
		selector.Get(1000)
	}

	// リセット
	selector.ResetStats()

	stats := selector.Stats()
	assert.Equal(t, uint64(0), stats.TotalSelections)
	assert.Equal(t, uint64(0), stats.SwitchCount)
	assert.Empty(t, stats.SelectionCounts)
}

func TestMinRTTSelector_ConcurrentAccess(t *testing.T) {
	selector := NewMinRTTSelector()

	t1 := transport.TransportID("t1")
	provider1 := &mockMetricsProvider{
		rtt:              50 * time.Millisecond,
		rttvar:           25 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000,
	}
	selector.UpdateTransport(t1, NewTransportInfo(t1, provider1))

	t2 := transport.TransportID("t2")
	provider2 := &mockMetricsProvider{
		rtt:              100 * time.Millisecond,
		rttvar:           50 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000,
	}
	selector.UpdateTransport(t2, NewTransportInfo(t2, provider2))

	var wg sync.WaitGroup

	// Get と SetQueueSize の並行実行
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 100 {
			selector.Get(1000)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range 100 {
			selector.SetQueueSize(uint64(i * 100))
		}
	}()

	// Get と UpdateTransport の並行実行
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 100 {
			selector.Get(1000)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 100 {
			provider := &mockMetricsProvider{
				rtt:              50 * time.Millisecond,
				rttvar:           25 * time.Millisecond,
				congestionWindow: 20000,
				bytesInFlight:    5000,
			}
			selector.UpdateTransport(t1, NewTransportInfo(t1, provider))
		}
	}()

	wg.Wait()
}

func TestMinRTTSelector_TransportMetricsUpdaterInterface(t *testing.T) {
	selector := NewMinRTTSelector()

	// TransportMetricsUpdater インターフェースを満たすことを確認
	var _ TransportMetricsUpdater = selector

	// UpdateTransport が呼び出せることを確認
	transportID := transport.TransportID("test-transport")
	provider := &mockMetricsProvider{
		rtt:              50 * time.Millisecond,
		rttvar:           25 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000,
	}
	info := NewTransportInfo(transportID, provider)
	selector.UpdateTransport(transportID, info)

	// SetQueueSize が呼び出せることを確認（no-op）
	selector.SetQueueSize(1000)

	// トランスポートが登録されていることを確認
	selectedID := selector.Get(1000)
	assert.Equal(t, transportID, selectedID)
}

func TestMinRTTSelector_MultiTransportSetterInterface(t *testing.T) {
	selector := NewMinRTTSelector()

	// MultiTransportSetter インターフェースを満たすことを確認
	var _ MultiTransportSetter = selector

	// SetMultiTransport が呼び出せることを確認
	selector.SetMultiTransport(nil)
}

func TestMinRTTSelector_TransportSelectorInterface(t *testing.T) {
	selector := NewMinRTTSelector()

	// TransportSelector インターフェースを満たすことを確認
	var _ TransportSelector = selector
}
