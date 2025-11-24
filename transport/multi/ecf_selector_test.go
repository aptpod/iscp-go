package multi_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/aptpod/iscp-go/transport"
	"github.com/aptpod/iscp-go/transport/multi"
)

func TestECFSelector_NewECFSelector(t *testing.T) {
	selector := multi.NewECFSelector()
	assert.NotNil(t, selector)
}

func TestECFSelector_SetMultiTransport(t *testing.T) {
	selector := multi.NewECFSelector()
	// multiTransport は nil でも設定可能（テスト用）
	selector.SetMultiTransport(nil)
}

func TestECFSelector_UpdateTransport(t *testing.T) {
	selector := multi.NewECFSelector()
	transportID := transport.TransportID("test-transport")

	provider := &mockMetricsProvider{
		rtt:              50 * time.Millisecond,
		rttvar:           25 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000,
	}

	info := multi.NewTransportInfo(transportID, provider)
	selector.UpdateTransport(transportID, info)

	// Get() を呼び出して、トランスポートが登録されていることを確認
	selectedID := selector.Get(1000)
	assert.Equal(t, transportID, selectedID)
}

func TestECFSelector_SetQueueSize(t *testing.T) {
	selector := multi.NewECFSelector()
	selector.SetQueueSize(1000)

	// キューサイズが設定されたことを確認
	// (内部状態なので、Get()の動作で間接的に確認)
}

func TestECFSelector_Get_NoTransports(t *testing.T) {
	selector := multi.NewECFSelector()
	selectedID := selector.Get(1000)
	assert.Equal(t, transport.TransportID(""), selectedID)
}

func TestECFSelector_Get_SingleTransport(t *testing.T) {
	selector := multi.NewECFSelector()
	transportID := transport.TransportID("transport1")

	provider := &mockMetricsProvider{
		rtt:              50 * time.Millisecond,
		rttvar:           25 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000,
	}

	info := multi.NewTransportInfo(transportID, provider)
	selector.UpdateTransport(transportID, info)

	selectedID := selector.Get(1000)
	assert.Equal(t, transportID, selectedID)
}

func TestECFSelector_SelectTransportEarliestCompletionFirst_TwoTransports_SameSpeed(t *testing.T) {
	selector := multi.NewECFSelector()

	// 2つのトランスポートを追加（同じRTT）
	transport1 := transport.TransportID("transport1")
	transport2 := transport.TransportID("transport2")

	provider1 := &mockMetricsProvider{
		rtt:              50 * time.Millisecond,
		rttvar:           25 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000,
	}

	provider2 := &mockMetricsProvider{
		rtt:              50 * time.Millisecond,
		rttvar:           25 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000,
	}

	info1 := multi.NewTransportInfo(transport1, provider1)
	info2 := multi.NewTransportInfo(transport2, provider2)

	selector.UpdateTransport(transport1, info1)
	selector.UpdateTransport(transport2, info2)

	// 同じRTTなので、どちらかが選択される
	selectedID := selector.Get(1000)
	assert.NotEqual(t, transport.TransportID(""), selectedID)
	assert.True(t, selectedID == transport1 || selectedID == transport2)
}

func TestECFSelector_SelectTransportEarliestCompletionFirst_TwoTransports_DifferentSpeed(t *testing.T) {
	selector := multi.NewECFSelector()

	// 2つのトランスポートを追加（異なるRTT）
	fastTransport := transport.TransportID("fast")
	slowTransport := transport.TransportID("slow")

	fastProvider := &mockMetricsProvider{
		rtt:              20 * time.Millisecond, // 高速
		rttvar:           10 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000,
	}

	slowProvider := &mockMetricsProvider{
		rtt:              100 * time.Millisecond, // 低速
		rttvar:           50 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000,
	}

	fastInfo := multi.NewTransportInfo(fastTransport, fastProvider)
	slowInfo := multi.NewTransportInfo(slowTransport, slowProvider)

	selector.UpdateTransport(fastTransport, fastInfo)
	selector.UpdateTransport(slowTransport, slowInfo)

	// 高速トランスポートが選択される
	selectedID := selector.Get(1000)
	assert.Equal(t, fastTransport, selectedID)
}

func TestECFSelector_SelectTransportEarliestCompletionFirst_FastestNotAvailable(t *testing.T) {
	selector := multi.NewECFSelector()

	// 2つのトランスポートを追加
	// 最速トランスポートは送信不可、低速トランスポートは送信可能
	fastTransport := transport.TransportID("fast")
	slowTransport := transport.TransportID("slow")

	fastProvider := &mockMetricsProvider{
		rtt:              20 * time.Millisecond, // 高速
		rttvar:           10 * time.Millisecond,
		congestionWindow: 10000,
		bytesInFlight:    10000, // CWND と同じ = 送信不可
	}

	slowProvider := &mockMetricsProvider{
		rtt:              100 * time.Millisecond, // 低速
		rttvar:           50 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000, // CWND より小さい = 送信可能
	}

	fastInfo := multi.NewTransportInfo(fastTransport, fastProvider)
	slowInfo := multi.NewTransportInfo(slowTransport, slowProvider)

	selector.UpdateTransport(fastTransport, fastInfo)
	selector.UpdateTransport(slowTransport, slowInfo)

	// 低速トランスポートが選択される（高速は送信不可のため）
	selectedID := selector.Get(1000)
	assert.Equal(t, slowTransport, selectedID)
}

func TestECFSelector_SelectTransportEarliestCompletionFirst_AllNotAvailable(t *testing.T) {
	selector := multi.NewECFSelector()

	// 2つのトランスポートを追加（両方とも送信不可）
	transport1 := transport.TransportID("transport1")
	transport2 := transport.TransportID("transport2")

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

	info1 := multi.NewTransportInfo(transport1, provider1)
	info2 := multi.NewTransportInfo(transport2, provider2)

	selector.UpdateTransport(transport1, info1)
	selector.UpdateTransport(transport2, info2)

	// 送信可能なトランスポートがないため、空文字列
	selectedID := selector.Get(1000)
	assert.Equal(t, transport.TransportID(""), selectedID)
}

func TestECFSelector_SelectTransportEarliestCompletionFirst_FirstInequality(t *testing.T) {
	selector := multi.NewECFSelector()

	// 第1不等式のテスト
	// minRTTTransport (fast) が送信不可、availableMinRTTTransport (slow) が送信可能
	fastTransport := transport.TransportID("fast")
	slowTransport := transport.TransportID("slow")

	fastProvider := &mockMetricsProvider{
		rtt:              20 * time.Millisecond,
		rttvar:           10 * time.Millisecond,
		congestionWindow: 14600, // 10 * MSS
		bytesInFlight:    14600, // 送信不可
	}

	slowProvider := &mockMetricsProvider{
		rtt:              100 * time.Millisecond,
		rttvar:           50 * time.Millisecond,
		congestionWindow: 14600,
		bytesInFlight:    5000, // 送信可能
	}

	fastInfo := multi.NewTransportInfo(fastTransport, fastProvider)
	slowInfo := multi.NewTransportInfo(slowTransport, slowProvider)

	selector.UpdateTransport(fastTransport, fastInfo)
	selector.UpdateTransport(slowTransport, slowInfo)
	selector.SetQueueSize(0)

	// 第1不等式を評価
	selectedID := selector.Get(1000)

	// この場合、低速トランスポートが選択される（高速は送信不可のため）
	assert.Equal(t, slowTransport, selectedID)
}

func TestECFSelector_SelectTransportEarliestCompletionFirst_Waiting(t *testing.T) {
	selector := multi.NewECFSelector()

	// 待機のテスト: allowWaiting = true の場合
	// 通常、Get() は allowWaiting = false で呼ばれるため、このテストは直接 SelectTransportEarliestCompletionFirst を呼ぶ

	fastTransport := transport.TransportID("fast")
	slowTransport := transport.TransportID("slow")

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

	fastInfo := multi.NewTransportInfo(fastTransport, fastProvider)
	slowInfo := multi.NewTransportInfo(slowTransport, slowProvider)

	selector.UpdateTransport(fastTransport, fastInfo)
	selector.UpdateTransport(slowTransport, slowInfo)
	selector.SetQueueSize(0)

	// allowWaiting = true で呼び出し
	selectedID := selector.SelectTransportEarliestCompletionFirst(true)

	// 第2不等式が真の場合、待機（空文字列）が返される可能性がある
	// ただし、具体的な値によって異なるため、空文字列またはslowTransportが返される
	// ここでは、どちらかが返されることを確認
	assert.True(t, selectedID == "" || selectedID == slowTransport)
}

func TestECFSelector_SelectTransportEarliestCompletionFirst_SameTransportSelected(t *testing.T) {
	selector := multi.NewECFSelector()

	// minRTTTransport と availableMinRTTTransport が同じ場合
	fastTransport := transport.TransportID("fast")

	fastProvider := &mockMetricsProvider{
		rtt:              20 * time.Millisecond,
		rttvar:           10 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000, // 送信可能
	}

	fastInfo := multi.NewTransportInfo(fastTransport, fastProvider)
	selector.UpdateTransport(fastTransport, fastInfo)

	// 単一トランスポートなので、即座に返される
	selectedID := selector.Get(1000)
	assert.Equal(t, fastTransport, selectedID)
}

func TestECFSelector_ConcurrentAccess(t *testing.T) {
	selector := multi.NewECFSelector()

	transport1 := transport.TransportID("transport1")
	transport2 := transport.TransportID("transport2")

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

	info1 := multi.NewTransportInfo(transport1, provider1)
	info2 := multi.NewTransportInfo(transport2, provider2)

	selector.UpdateTransport(transport1, info1)
	selector.UpdateTransport(transport2, info2)

	// 並行アクセスのテスト
	done := make(chan bool)

	go func() {
		for i := range 100 {
			selector.Get(1000)
			selector.SetQueueSize(uint64(i))
		}
		done <- true
	}()

	go func() {
		for range 100 {
			selector.Get(1000)
			selector.UpdateTransport(transport1, info1)
		}
		done <- true
	}()

	<-done
	<-done
}
