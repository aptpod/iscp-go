package multi_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aptpod/iscp-go/transport"
	. "github.com/aptpod/iscp-go/transport/multi"
)

// =============================================================================
// 1. 基本的なGet()のパフォーマンス（トランスポート数別）
// =============================================================================

func BenchmarkMinRTTSelector_Get_SingleTransport(b *testing.B) {
	selector := NewMinRTTSelector()
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

func BenchmarkMinRTTSelector_Get_TwoTransports(b *testing.B) {
	selector := NewMinRTTSelector()

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

func BenchmarkMinRTTSelector_Get_FourTransports(b *testing.B) {
	selector := NewMinRTTSelector()

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
func BenchmarkMinRTTSelector_Get_TransportCount(b *testing.B) {
	counts := []int{1, 2, 4, 8}
	for _, count := range counts {
		b.Run(fmt.Sprintf("count=%d", count), func(b *testing.B) {
			selector := NewMinRTTSelector()
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
// =============================================================================

func BenchmarkMinRTTSelector_Get_Parallel(b *testing.B) {
	parallelisms := []int{1, 2, 4, 8, 16}
	for _, p := range parallelisms {
		b.Run(fmt.Sprintf("goroutines=%d", p), func(b *testing.B) {
			selector := NewMinRTTSelector()

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
// =============================================================================

func BenchmarkMinRTTSelector_MixedReadWrite(b *testing.B) {
	selector := NewMinRTTSelector()

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
// =============================================================================

func BenchmarkMinRTTSelector_Stats(b *testing.B) {
	selector := NewMinRTTSelector()

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

func BenchmarkMinRTTSelector_UpdateTransport(b *testing.B) {
	selector := NewMinRTTSelector()
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
// 6. SetQueueSizeのパフォーマンス（no-op確認）
// =============================================================================

func BenchmarkMinRTTSelector_SetQueueSize(b *testing.B) {
	selector := NewMinRTTSelector()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		selector.SetQueueSize(uint64(i % 10000))
	}
}

// =============================================================================
// 7. 高負荷シナリオ：実際の使用パターンをシミュレート
// =============================================================================

func BenchmarkMinRTTSelector_RealisticWorkload(b *testing.B) {
	selector := NewMinRTTSelector()

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
		// 1. キューサイズ更新（MinRTTではno-op）
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

// =============================================================================
// 8. ECFSelector との比較ベンチマーク
// =============================================================================

func BenchmarkSelector_Comparison_TwoTransports(b *testing.B) {
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

	b.Run("MinRTTSelector", func(b *testing.B) {
		selector := NewMinRTTSelector()
		selector.UpdateTransport(fast, NewTransportInfo(fast, fastProvider))
		selector.UpdateTransport(slow, NewTransportInfo(slow, slowProvider))

		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			selector.Get(1000)
		}
	})

	b.Run("ECFSelector", func(b *testing.B) {
		selector := NewECFSelector()
		selector.UpdateTransport(fast, NewTransportInfo(fast, fastProvider))
		selector.UpdateTransport(slow, NewTransportInfo(slow, slowProvider))

		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			selector.Get(1000)
		}
	})
}

func BenchmarkSelector_Comparison_Parallel(b *testing.B) {
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

	b.Run("MinRTTSelector", func(b *testing.B) {
		selector := NewMinRTTSelector()
		selector.UpdateTransport(fast, NewTransportInfo(fast, fastProvider))
		selector.UpdateTransport(slow, NewTransportInfo(slow, slowProvider))

		b.ResetTimer()
		b.ReportAllocs()
		b.SetParallelism(8)
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				selector.Get(1000)
			}
		})
	})

	b.Run("ECFSelector", func(b *testing.B) {
		selector := NewECFSelector()
		selector.UpdateTransport(fast, NewTransportInfo(fast, fastProvider))
		selector.UpdateTransport(slow, NewTransportInfo(slow, slowProvider))

		b.ResetTimer()
		b.ReportAllocs()
		b.SetParallelism(8)
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				selector.Get(1000)
			}
		})
	})
}
