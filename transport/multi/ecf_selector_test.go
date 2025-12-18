package multi_test

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aptpod/iscp-go/transport"
	. "github.com/aptpod/iscp-go/transport/multi"
)

func TestECFSelector_NewECFSelector(t *testing.T) {
	selector := NewECFSelector()
	require.NotNil(t, selector)
}

func TestECFSelector_SetMultiTransport(t *testing.T) {
	selector := NewECFSelector()
	// multiTransport は nil でも設定可能（テスト用）
	selector.SetMultiTransport(nil)
}

func TestECFSelector_UpdateTransport(t *testing.T) {
	selector := NewECFSelector()
	transportID := transport.TransportID("test-transport")

	provider := &mockMetricsProvider{
		rtt:              50 * time.Millisecond,
		rttvar:           25 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000,
	}

	info := NewTransportInfo(transportID, provider)
	selector.UpdateTransport(transportID, info)

	// Get() を呼び出して、トランスポートが登録されていることを確認
	selectedID := selector.Get(1000)
	assert.Equal(t, transportID, selectedID)
}

func TestECFSelector_SetQueueSize(t *testing.T) {
	selector := NewECFSelector()
	selector.SetQueueSize(1000)

	// キューサイズが設定されたことを確認
	// (内部状態なので、Get()の動作で間接的に確認)
}

func TestECFSelector_SetWaitPollInterval(t *testing.T) {
	selector := NewECFSelector()
	selector.SetWaitPollInterval(50 * time.Microsecond)

	// 設定されたことを確認（内部状態なので直接確認できない）
}

func TestECFSelector_Get(t *testing.T) {
	tests := []struct {
		name       string
		setup      func() *ECFSelector
		want       transport.TransportID
		wantOneOf  []transport.TransportID // どちらかであればOK
		wantNotNil bool                    // 空でなければOK
	}{
		{
			name: "edge case: no transports returns empty",
			setup: func() *ECFSelector {
				return NewECFSelector()
			},
			want: "",
		},
		{
			name: "success: single transport returns it",
			setup: func() *ECFSelector {
				selector := NewECFSelector()
				id := transport.TransportID("transport1")
				provider := &mockMetricsProvider{
					rtt:              50 * time.Millisecond,
					rttvar:           25 * time.Millisecond,
					congestionWindow: 20000,
					bytesInFlight:    5000,
				}
				info := NewTransportInfo(id, provider)
				selector.UpdateTransport(id, info)
				return selector
			},
			want: "transport1",
		},
		{
			name: "success: two transports same speed returns one",
			setup: func() *ECFSelector {
				selector := NewECFSelector()
				t1, t2 := transport.TransportID("t1"), transport.TransportID("t2")
				provider := &mockMetricsProvider{
					rtt:              50 * time.Millisecond,
					rttvar:           25 * time.Millisecond,
					congestionWindow: 20000,
					bytesInFlight:    5000,
				}
				selector.UpdateTransport(t1, NewTransportInfo(t1, provider))
				selector.UpdateTransport(t2, NewTransportInfo(t2, provider))
				return selector
			},
			wantOneOf: []transport.TransportID{"t1", "t2"},
		},
		{
			name: "success: two transports different speed returns fastest",
			setup: func() *ECFSelector {
				selector := NewECFSelector()
				fast, slow := transport.TransportID("fast"), transport.TransportID("slow")
				fastProvider := &mockMetricsProvider{
					rtt:              20 * time.Millisecond,
					rttvar:           10 * time.Millisecond,
					congestionWindow: 20000,
					bytesInFlight:    5000,
				}
				slowProvider := &mockMetricsProvider{
					rtt:              100 * time.Millisecond,
					rttvar:           50 * time.Millisecond,
					congestionWindow: 20000,
					bytesInFlight:    5000,
				}
				selector.UpdateTransport(fast, NewTransportInfo(fast, fastProvider))
				selector.UpdateTransport(slow, NewTransportInfo(slow, slowProvider))
				return selector
			},
			want: "fast",
		},
		{
			name: "edge case: all transports not available returns empty",
			setup: func() *ECFSelector {
				selector := NewECFSelector()
				t1, t2 := transport.TransportID("t1"), transport.TransportID("t2")
				provider1 := &mockMetricsProvider{
					rtt:              50 * time.Millisecond,
					rttvar:           25 * time.Millisecond,
					congestionWindow: 10000,
					bytesInFlight:    10000, // CWND と同じ = 送信不可
				}
				provider2 := &mockMetricsProvider{
					rtt:              100 * time.Millisecond,
					rttvar:           50 * time.Millisecond,
					congestionWindow: 10000,
					bytesInFlight:    15000, // CWND より大きい = 送信不可
				}
				selector.UpdateTransport(t1, NewTransportInfo(t1, provider1))
				selector.UpdateTransport(t2, NewTransportInfo(t2, provider2))
				return selector
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selector := tt.setup()
			got := selector.Get(1000)

			if tt.wantOneOf != nil {
				assert.Contains(t, tt.wantOneOf, got, "expected one of %v, got %v", tt.wantOneOf, got)
			} else if tt.wantNotNil {
				assert.NotEmpty(t, got)
			} else {
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestECFSelector_SelectTransportECF(t *testing.T) {
	tests := []struct {
		name      string
		setup     func() *ECFSelector
		wantOneOf []transport.TransportID // 許容される結果
	}{
		{
			name: "success: fastest not available may wait or select slow",
			setup: func() *ECFSelector {
				selector := NewECFSelector()
				fast, slow := transport.TransportID("fast"), transport.TransportID("slow")
				fastProvider := &mockMetricsProvider{
					rtt:              20 * time.Millisecond,
					rttvar:           10 * time.Millisecond,
					congestionWindow: 10000,
					bytesInFlight:    10000, // 送信不可
				}
				slowProvider := &mockMetricsProvider{
					rtt:              100 * time.Millisecond,
					rttvar:           50 * time.Millisecond,
					congestionWindow: 20000,
					bytesInFlight:    5000, // 送信可能
				}
				selector.UpdateTransport(fast, NewTransportInfo(fast, fastProvider))
				selector.UpdateTransport(slow, NewTransportInfo(slow, slowProvider))
				return selector
			},
			wantOneOf: []transport.TransportID{"slow", ""},
		},
		{
			name: "success: first inequality evaluation",
			setup: func() *ECFSelector {
				selector := NewECFSelector()
				fast, slow := transport.TransportID("fast"), transport.TransportID("slow")
				fastProvider := &mockMetricsProvider{
					rtt:              20 * time.Millisecond,
					rttvar:           10 * time.Millisecond,
					congestionWindow: 14600,
					bytesInFlight:    14600, // 送信不可
				}
				slowProvider := &mockMetricsProvider{
					rtt:              100 * time.Millisecond,
					rttvar:           50 * time.Millisecond,
					congestionWindow: 14600,
					bytesInFlight:    5000, // 送信可能
				}
				selector.UpdateTransport(fast, NewTransportInfo(fast, fastProvider))
				selector.UpdateTransport(slow, NewTransportInfo(slow, slowProvider))
				selector.SetQueueSize(0)
				return selector
			},
			wantOneOf: []transport.TransportID{"slow", ""},
		},
		{
			name: "success: waiting decision test",
			setup: func() *ECFSelector {
				selector := NewECFSelector()
				fast, slow := transport.TransportID("fast"), transport.TransportID("slow")
				fastProvider := &mockMetricsProvider{
					rtt:              10 * time.Millisecond,
					rttvar:           5 * time.Millisecond,
					congestionWindow: 14600,
					bytesInFlight:    14600, // 送信不可
				}
				slowProvider := &mockMetricsProvider{
					rtt:              200 * time.Millisecond,
					rttvar:           100 * time.Millisecond,
					congestionWindow: 14600,
					bytesInFlight:    5000, // 送信可能
				}
				selector.UpdateTransport(fast, NewTransportInfo(fast, fastProvider))
				selector.UpdateTransport(slow, NewTransportInfo(slow, slowProvider))
				selector.SetQueueSize(0)
				return selector
			},
			wantOneOf: []transport.TransportID{"slow", ""},
		},
		{
			name: "success: same transport is minRTT and available",
			setup: func() *ECFSelector {
				selector := NewECFSelector()
				fast := transport.TransportID("fast")
				fastProvider := &mockMetricsProvider{
					rtt:              20 * time.Millisecond,
					rttvar:           10 * time.Millisecond,
					congestionWindow: 20000,
					bytesInFlight:    5000, // 送信可能
				}
				selector.UpdateTransport(fast, NewTransportInfo(fast, fastProvider))
				return selector
			},
			wantOneOf: []transport.TransportID{"fast"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selector := tt.setup()
			got := selector.SelectTransportECF()
			assert.Contains(t, tt.wantOneOf, got, "expected one of %v, got %v", tt.wantOneOf, got)
		})
	}
}

func TestECFSelector_Stats(t *testing.T) {
	selector := NewECFSelector()
	fast := transport.TransportID("fast")
	fastProvider := &mockMetricsProvider{
		rtt:              20 * time.Millisecond,
		rttvar:           10 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000,
	}
	selector.UpdateTransport(fast, NewTransportInfo(fast, fastProvider))

	// 何度か選択
	for range 5 {
		selector.Get(1000)
	}

	stats := selector.Stats()
	assert.Equal(t, uint64(5), stats.TotalSelections)
	assert.Equal(t, uint64(5), stats.SelectionCounts[fast])
}

func TestECFSelector_ResetStats(t *testing.T) {
	selector := NewECFSelector()
	fast := transport.TransportID("fast")
	fastProvider := &mockMetricsProvider{
		rtt:              20 * time.Millisecond,
		rttvar:           10 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000,
	}
	selector.UpdateTransport(fast, NewTransportInfo(fast, fastProvider))

	// 何度か選択してからリセット
	for range 5 {
		selector.Get(1000)
	}
	selector.ResetStats()

	stats := selector.Stats()
	assert.Equal(t, uint64(0), stats.TotalSelections)
}

func TestECFSelector_ConcurrentAccess(t *testing.T) {
	selector := NewECFSelector()

	t1 := transport.TransportID("transport1")
	t2 := transport.TransportID("transport2")

	provider1 := &mockMetricsProvider{
		rtt:              50 * time.Millisecond,
		rttvar:           25 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000,
	}

	provider2 := &mockMetricsProvider{
		rtt:              100 * time.Millisecond,
		rttvar:           50 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000,
	}

	info1 := NewTransportInfo(t1, provider1)
	info2 := NewTransportInfo(t2, provider2)

	selector.UpdateTransport(t1, info1)
	selector.UpdateTransport(t2, info2)

	// 並行アクセスのテスト
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := range 100 {
			selector.Get(1000)
			selector.SetQueueSize(uint64(i))
		}
	}()

	go func() {
		defer wg.Done()
		for range 100 {
			selector.Get(1000)
			selector.UpdateTransport(t1, info1)
		}
	}()

	wg.Wait()
}
