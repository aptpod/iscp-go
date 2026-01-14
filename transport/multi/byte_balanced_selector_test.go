package multi_test

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aptpod/iscp-go/transport"
	. "github.com/aptpod/iscp-go/transport/multi"
)

func TestByteBalancedSelector_NewByteBalancedSelector(t *testing.T) {
	transportIDs := []transport.TransportID{"t1", "t2", "t3"}
	selector := NewByteBalancedSelector(transportIDs)

	require.NotNil(t, selector)
}

func TestByteBalancedSelector_SetMultiTransport(t *testing.T) {
	selector := NewByteBalancedSelector([]transport.TransportID{"t1"})

	// nil を設定しても panic しないことを確認
	selector.SetMultiTransport(nil)
}

func TestByteBalancedSelector_Get_EmptyTransportIDs(t *testing.T) {
	selector := NewByteBalancedSelector([]transport.TransportID{})

	// 空の場合は空文字を返す
	assert.Equal(t, transport.TransportID(""), selector.Get(0))
}

func TestByteBalancedSelector_Get_SingleTransportID(t *testing.T) {
	selector := NewByteBalancedSelector([]transport.TransportID{"t1"})

	// 単一のトランスポートの場合、それを返す
	assert.Equal(t, transport.TransportID("t1"), selector.Get(0))
	assert.Equal(t, transport.TransportID("t1"), selector.Get(0))
	assert.Equal(t, transport.TransportID("t1"), selector.Get(0))
}

func TestByteBalancedSelector_Get_MultipleTransportIDs_WithoutMultiTransport(t *testing.T) {
	transportIDs := []transport.TransportID{"t1", "t2", "t3"}
	selector := NewByteBalancedSelector(transportIDs)

	// multiTransportが未設定の場合、最初のトランスポートを返す
	assert.Equal(t, transport.TransportID("t1"), selector.Get(0))
	assert.Equal(t, transport.TransportID("t1"), selector.Get(0))
}

func TestByteBalancedSelector_Stats(t *testing.T) {
	selector := NewByteBalancedSelector([]transport.TransportID{"t1"})

	// 5回選択
	for range 5 {
		selector.Get(0)
	}

	stats := selector.Stats()
	assert.Equal(t, uint64(5), stats.TotalSelections)
	assert.Equal(t, uint64(5), stats.SelectionCounts["t1"])
	assert.Equal(t, uint64(0), stats.SwitchCount) // 同じトランスポートなのでスイッチなし
}

func TestByteBalancedSelector_ResetStats(t *testing.T) {
	selector := NewByteBalancedSelector([]transport.TransportID{"t1"})

	// 選択を実行
	for range 5 {
		selector.Get(0)
	}

	// リセット
	selector.ResetStats()

	stats := selector.Stats()
	assert.Equal(t, uint64(0), stats.TotalSelections)
	assert.Equal(t, uint64(0), stats.SwitchCount)
	assert.Empty(t, stats.SelectionCounts)
}

func TestByteBalancedSelector_TransportSelectorInterface(t *testing.T) {
	selector := NewByteBalancedSelector([]transport.TransportID{"t1"})

	// TransportSelector インターフェースを満たすことを確認
	var _ TransportSelector = selector
}

func TestByteBalancedSelector_MultiTransportSetterInterface(t *testing.T) {
	selector := NewByteBalancedSelector([]transport.TransportID{"t1"})

	// MultiTransportSetter インターフェースを満たすことを確認
	var _ MultiTransportSetter = selector
}

func TestByteBalancedSelector_ConcurrentAccess_Get(t *testing.T) {
	transportIDs := []transport.TransportID{"t1", "t2", "t3"}
	selector := NewByteBalancedSelector(transportIDs)

	var wg sync.WaitGroup

	// 複数 goroutine からの並行 Get 呼び出し
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				selector.Get(0)
			}
		}()
	}

	wg.Wait()
}

func TestByteBalancedSelector_ConcurrentAccess_SetMultiTransportAndGet(t *testing.T) {
	transportIDs := []transport.TransportID{"t1", "t2"}
	selector := NewByteBalancedSelector(transportIDs)

	var wg sync.WaitGroup

	// Get と SetMultiTransport の並行実行
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 100 {
			selector.Get(0)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 100 {
			selector.SetMultiTransport(nil)
		}
	}()

	wg.Wait()
}

func TestByteBalancedSelector_ConcurrentAccess_StatsAndGet(t *testing.T) {
	transportIDs := []transport.TransportID{"t1", "t2"}
	selector := NewByteBalancedSelector(transportIDs)

	var wg sync.WaitGroup

	// Get と Stats の並行実行
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 100 {
			selector.Get(0)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 100 {
			_ = selector.Stats()
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 100 {
			selector.ResetStats()
		}
	}()

	wg.Wait()
}
