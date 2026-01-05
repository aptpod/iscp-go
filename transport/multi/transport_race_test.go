package multi

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aptpod/iscp-go/log"
	"github.com/aptpod/iscp-go/transport"
	"github.com/aptpod/iscp-go/transport/reconnect"
)

// mockReconnectTransport は reconnect.Transport のモックです。
// 競合状態のテストに必要な最小限のインターフェースを実装します。
type mockReconnectTransport struct {
	txBytes uint64
	rxBytes uint64
}

func (m *mockReconnectTransport) TxBytesCounterValue() uint64 {
	return m.txBytes
}

func (m *mockReconnectTransport) RxBytesCounterValue() uint64 {
	return m.rxBytes
}

func (m *mockReconnectTransport) Name() transport.Name {
	return "mock"
}

// TestTxBytesCounterValueRaceCondition は、TxBytesCounterValue() の
// 競合状態を検証します。
// go test -race で実行することで競合を検出します。
//
// 期待結果:
//   - 修正前: WARNING: DATA RACE が検出される
//   - 修正後: 競合なしでテストがパスする
func TestTxBytesCounterValueRaceCondition(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mt := &Transport{
		ctx:          ctx,
		cancel:       cancel,
		transportMap: make(map[transport.TransportID]*reconnect.Transport),
		logger:       log.NewNop(),
	}

	var wg sync.WaitGroup
	iterations := 1000

	// Goroutine 1: TxBytesCounterValue と RxBytesCounterValue を繰り返し呼び出す
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_ = mt.TxBytesCounterValue()
			_ = mt.RxBytesCounterValue()
		}
	}()

	// Goroutine 2: transportMap への追加/削除をシミュレート
	// （実際の実装では readLoopTransport の defer で削除される）
	// 注意: nil は使用せず、別のキーの追加/削除でマップ操作の競合をテスト
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			mt.mu.Lock()
			// 追加と即削除（別のキーを使用）
			// これによりマップの内部構造が変更され、競合状態が発生しうる
			key := transport.TransportID(fmt.Sprintf("temp-%d", i%10))
			delete(mt.transportMap, key)
			mt.mu.Unlock()

			time.Sleep(time.Microsecond)

			mt.mu.Lock()
			delete(mt.transportMap, key)
			mt.mu.Unlock()
		}
	}()

	wg.Wait()
	// -race フラグで競合が検出されればテスト失敗
}

// TestRxBytesCounterValueRaceCondition は、RxBytesCounterValue() の
// 競合状態を検証します。
func TestRxBytesCounterValueRaceCondition(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mt := &Transport{
		ctx:          ctx,
		cancel:       cancel,
		transportMap: make(map[transport.TransportID]*reconnect.Transport),
		logger:       log.NewNop(),
	}

	var wg sync.WaitGroup
	iterations := 1000

	// Goroutine 1: RxBytesCounterValue を繰り返し呼び出す
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_ = mt.RxBytesCounterValue()
		}
	}()

	// Goroutine 2: transportMap への追加/削除
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			mt.mu.Lock()
			key := transport.TransportID(fmt.Sprintf("temp-%d", i%10))
			delete(mt.transportMap, key)
			mt.mu.Unlock()

			mt.mu.Lock()
			delete(mt.transportMap, key)
			mt.mu.Unlock()
		}
	}()

	wg.Wait()
}

// TestNameRaceCondition は Name() の競合状態を検証します。
func TestNameRaceCondition(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mt := &Transport{
		ctx:          ctx,
		cancel:       cancel,
		transportMap: make(map[transport.TransportID]*reconnect.Transport),
		logger:       log.NewNop(),
	}

	var wg sync.WaitGroup
	iterations := 1000

	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_ = mt.Name()
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			mt.mu.Lock()
			key := transport.TransportID(fmt.Sprintf("temp-%d", i%10))
			delete(mt.transportMap, key)
			mt.mu.Unlock()

			mt.mu.Lock()
			delete(mt.transportMap, key)
			mt.mu.Unlock()
		}
	}()

	wg.Wait()
}
