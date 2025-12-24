package multi_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aptpod/iscp-go/transport"
	. "github.com/aptpod/iscp-go/transport/multi"
)

// benchmarkMetricsProvider はベンチマーク用の軽量なMetricsProvider実装です。
type benchmarkMetricsProvider struct {
	rtt              time.Duration
	rttvar           time.Duration
	congestionWindow uint64
	bytesInFlight    uint64
}

func (m *benchmarkMetricsProvider) RTT() time.Duration       { return m.rtt }
func (m *benchmarkMetricsProvider) RTTVar() time.Duration    { return m.rttvar }
func (m *benchmarkMetricsProvider) CongestionWindow() uint64 { return m.congestionWindow }
func (m *benchmarkMetricsProvider) BytesInFlight() uint64    { return m.bytesInFlight }
func (m *benchmarkMetricsProvider) Start() error             { return nil }
func (m *benchmarkMetricsProvider) Stop()                    {}

// =============================================================================
// 1. 基本的なGet()のパフォーマンス（トランスポート数別）
//    問題点: マップアロケーション、2重ループ
// =============================================================================

func BenchmarkECFSelector_Get_SingleTransport(b *testing.B) {
	selector := NewECFSelector()
	id := transport.TransportID("t1")
	provider := &benchmarkMetricsProvider{
		rtt:              50 * time.Millisecond,
		rttvar:           25 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000,
	}
	selector.UpdateTransport(id, NewTransportInfo(id, provider))

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		selector.Get(1000)
	}
}

func BenchmarkECFSelector_Get_TwoTransports(b *testing.B) {
	selector := NewECFSelector()

	fastProvider := &benchmarkMetricsProvider{
		rtt:              20 * time.Millisecond,
		rttvar:           10 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000,
	}
	slowProvider := &benchmarkMetricsProvider{
		rtt:              100 * time.Millisecond,
		rttvar:           50 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000,
	}

	fast := transport.TransportID("fast")
	slow := transport.TransportID("slow")
	selector.UpdateTransport(fast, NewTransportInfo(fast, fastProvider))
	selector.UpdateTransport(slow, NewTransportInfo(slow, slowProvider))

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		selector.Get(1000)
	}
}

func BenchmarkECFSelector_Get_FourTransports(b *testing.B) {
	selector := NewECFSelector()

	rtts := []time.Duration{20, 50, 80, 120}
	for i, rtt := range rtts {
		id := transport.TransportID(fmt.Sprintf("t%d", i))
		provider := &benchmarkMetricsProvider{
			rtt:              rtt * time.Millisecond,
			rttvar:           rtt / 2 * time.Millisecond,
			congestionWindow: 20000,
			bytesInFlight:    5000,
		}
		selector.UpdateTransport(id, NewTransportInfo(id, provider))
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		selector.Get(1000)
	}
}

// トランスポート数を変えてアロケーションを比較
func BenchmarkECFSelector_Get_TransportCount(b *testing.B) {
	counts := []int{1, 2, 4, 8}
	for _, count := range counts {
		b.Run(fmt.Sprintf("count=%d", count), func(b *testing.B) {
			selector := NewECFSelector()
			for i := 0; i < count; i++ {
				id := transport.TransportID(fmt.Sprintf("t%d", i))
				provider := &benchmarkMetricsProvider{
					rtt:              time.Duration(20+i*10) * time.Millisecond,
					rttvar:           time.Duration(10+i*5) * time.Millisecond,
					congestionWindow: 20000,
					bytesInFlight:    5000,
				}
				selector.UpdateTransport(id, NewTransportInfo(id, provider))
			}

			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				selector.Get(1000)
			}
		})
	}
}

// =============================================================================
// 2. 並行アクセス時のパフォーマンス（ロック競合の測定）
//    問題点: selectTransportECF全体をLock、Get内で複数回RLock/RUnlock
// =============================================================================

func BenchmarkECFSelector_Get_Parallel(b *testing.B) {
	parallelisms := []int{1, 2, 4, 8, 16}
	for _, p := range parallelisms {
		b.Run(fmt.Sprintf("goroutines=%d", p), func(b *testing.B) {
			selector := NewECFSelector()

			fastProvider := &benchmarkMetricsProvider{
				rtt:              20 * time.Millisecond,
				rttvar:           10 * time.Millisecond,
				congestionWindow: 20000,
				bytesInFlight:    5000,
			}
			slowProvider := &benchmarkMetricsProvider{
				rtt:              100 * time.Millisecond,
				rttvar:           50 * time.Millisecond,
				congestionWindow: 20000,
				bytesInFlight:    5000,
			}

			fast := transport.TransportID("fast")
			slow := transport.TransportID("slow")
			selector.UpdateTransport(fast, NewTransportInfo(fast, fastProvider))
			selector.UpdateTransport(slow, NewTransportInfo(slow, slowProvider))

			b.ResetTimer()
			b.ReportAllocs()
			b.SetParallelism(p)
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					selector.Get(1000)
				}
			})
		})
	}
}

// =============================================================================
// 3. 読み書き混合パターン（UpdateTransport + Get の競合）
//    問題点: UpdateTransportとGetがロックを取り合う
// =============================================================================

func BenchmarkECFSelector_MixedReadWrite(b *testing.B) {
	selector := NewECFSelector()

	fastProvider := &benchmarkMetricsProvider{
		rtt:              20 * time.Millisecond,
		rttvar:           10 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000,
	}
	slowProvider := &benchmarkMetricsProvider{
		rtt:              100 * time.Millisecond,
		rttvar:           50 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000,
	}

	fast := transport.TransportID("fast")
	slow := transport.TransportID("slow")
	selector.UpdateTransport(fast, NewTransportInfo(fast, fastProvider))
	selector.UpdateTransport(slow, NewTransportInfo(slow, slowProvider))

	b.ResetTimer()
	b.ReportAllocs()

	var wg sync.WaitGroup
	stopCh := make(chan struct{})

	// 読み込みgoroutine（高頻度）
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stopCh:
					return
				default:
					selector.Get(1000)
				}
			}
		}()
	}

	// 書き込みgoroutine（メトリクス更新）
	wg.Add(1)
	go func() {
		defer wg.Done()
		providers := []*benchmarkMetricsProvider{
			{rtt: 20 * time.Millisecond, rttvar: 10 * time.Millisecond, congestionWindow: 20000, bytesInFlight: 5000},
			{rtt: 25 * time.Millisecond, rttvar: 12 * time.Millisecond, congestionWindow: 18000, bytesInFlight: 6000},
		}
		idx := 0
		for {
			select {
			case <-stopCh:
				return
			default:
				selector.UpdateTransport(fast, NewTransportInfo(fast, providers[idx%2]))
				idx++
				time.Sleep(100 * time.Microsecond)
			}
		}
	}()

	// 実測
	for i := 0; i < b.N; i++ {
		selector.Get(1000)
	}

	close(stopCh)
	wg.Wait()
}

// =============================================================================
// 4. Stats取得のパフォーマンス
//    問題点: maps.Copyによるアロケーション
// =============================================================================

func BenchmarkECFSelector_Stats(b *testing.B) {
	selector := NewECFSelector()

	for i := 0; i < 4; i++ {
		id := transport.TransportID(fmt.Sprintf("t%d", i))
		provider := &benchmarkMetricsProvider{
			rtt:              time.Duration(20+i*10) * time.Millisecond,
			rttvar:           time.Duration(10+i*5) * time.Millisecond,
			congestionWindow: 20000,
			bytesInFlight:    5000,
		}
		selector.UpdateTransport(id, NewTransportInfo(id, provider))
	}

	// 統計を蓄積
	for i := 0; i < 1000; i++ {
		selector.Get(1000)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = selector.Stats()
	}
}

// =============================================================================
// 5. UpdateTransport単体のパフォーマンス
// =============================================================================

func BenchmarkECFSelector_UpdateTransport(b *testing.B) {
	selector := NewECFSelector()
	id := transport.TransportID("t1")
	provider := &benchmarkMetricsProvider{
		rtt:              50 * time.Millisecond,
		rttvar:           25 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000,
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		selector.UpdateTransport(id, NewTransportInfo(id, provider))
	}
}

// =============================================================================
// 6. selectTransportECF（内部メソッド）のパフォーマンス
//    export_test.goでエクスポート済み
// =============================================================================

func BenchmarkECFSelector_SelectTransportECF_TwoTransports(b *testing.B) {
	selector := NewECFSelector()

	fastProvider := &benchmarkMetricsProvider{
		rtt:              20 * time.Millisecond,
		rttvar:           10 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000,
	}
	slowProvider := &benchmarkMetricsProvider{
		rtt:              100 * time.Millisecond,
		rttvar:           50 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000,
	}

	fast := transport.TransportID("fast")
	slow := transport.TransportID("slow")
	selector.UpdateTransport(fast, NewTransportInfo(fast, fastProvider))
	selector.UpdateTransport(slow, NewTransportInfo(slow, slowProvider))

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		selector.SelectTransportECF()
	}
}

// =============================================================================
// 7. SetQueueSizeのパフォーマンス（頻繁に呼ばれる可能性がある）
// =============================================================================

func BenchmarkECFSelector_SetQueueSize(b *testing.B) {
	selector := NewECFSelector()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		selector.SetQueueSize(uint64(i % 10000))
	}
}

// =============================================================================
// 8. 高負荷シナリオ：実際の使用パターンをシミュレート
// =============================================================================

func BenchmarkECFSelector_RealisticWorkload(b *testing.B) {
	selector := NewECFSelector()

	// 2つのトランスポートを設定
	providers := []*benchmarkMetricsProvider{
		{rtt: 20 * time.Millisecond, rttvar: 10 * time.Millisecond, congestionWindow: 20000, bytesInFlight: 5000},
		{rtt: 80 * time.Millisecond, rttvar: 40 * time.Millisecond, congestionWindow: 15000, bytesInFlight: 3000},
	}
	ids := []transport.TransportID{"primary", "secondary"}

	for i, id := range ids {
		selector.UpdateTransport(id, NewTransportInfo(id, providers[i]))
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		// 実際の使用パターン：
		// 1. キューサイズ更新
		// 2. トランスポート選択
		// 3. たまにメトリクス更新
		selector.SetQueueSize(uint64(i % 1000))
		selector.Get(1000)

		if i%100 == 0 {
			// 100回に1回メトリクス更新
			providers[0].bytesInFlight = uint64(5000 + i%1000)
			selector.UpdateTransport(ids[0], NewTransportInfo(ids[0], providers[0]))
		}
	}
}
