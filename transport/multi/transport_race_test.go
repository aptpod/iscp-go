package multi

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aptpod/iscp-go/v2/log"
	"github.com/aptpod/iscp-go/v2/transport"
	"github.com/aptpod/iscp-go/v2/transport/reconnect"
)

// mockReconnectTransport is a mock of reconnect.Transport.
// It implements the minimal interface required for the race condition tests.
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

// TestTxBytesCounterValueRaceCondition verifies the race condition
// in TxBytesCounterValue().
// Run with go test -race to detect the race.
//
// Expected result:
//   - Before the fix: WARNING: DATA RACE is detected
//   - After the fix: the test passes with no race
func TestTxBytesCounterValueRaceCondition(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mt := &Transport{
		ctx:          ctx,
		cancel:       cancel,
		transportMap: make(map[transport.SubConnectionID]*reconnect.Transport),
		logger:       log.NewNop(),
	}

	var wg sync.WaitGroup
	iterations := 1000

	// Goroutine 1: repeatedly call TxBytesCounterValue and RxBytesCounterValue
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_ = mt.TxBytesCounterValue()
			_ = mt.RxBytesCounterValue()
		}
	}()

	// Goroutine 2: simulate adding to / deleting from transportMap
	// (in the real implementation, entries are deleted in the defer of readLoopTransport)
	// Note: do not use nil; test the map operation race by adding/deleting a different key
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			mt.mu.Lock()
			// Add and delete immediately (using a different key)
			// This changes the internal structure of the map, so a race can occur
			key := transport.SubConnectionID(fmt.Sprintf("temp-%d", i%10))
			delete(mt.transportMap, key)
			mt.mu.Unlock()

			time.Sleep(time.Microsecond)

			mt.mu.Lock()
			delete(mt.transportMap, key)
			mt.mu.Unlock()
		}
	}()

	wg.Wait()
	// The test fails if the -race flag detects a race
}

// TestRxBytesCounterValueRaceCondition verifies the race condition
// in RxBytesCounterValue().
func TestRxBytesCounterValueRaceCondition(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mt := &Transport{
		ctx:          ctx,
		cancel:       cancel,
		transportMap: make(map[transport.SubConnectionID]*reconnect.Transport),
		logger:       log.NewNop(),
	}

	var wg sync.WaitGroup
	iterations := 1000

	// Goroutine 1: repeatedly call RxBytesCounterValue
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_ = mt.RxBytesCounterValue()
		}
	}()

	// Goroutine 2: add to / delete from transportMap
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			mt.mu.Lock()
			key := transport.SubConnectionID(fmt.Sprintf("temp-%d", i%10))
			delete(mt.transportMap, key)
			mt.mu.Unlock()

			mt.mu.Lock()
			delete(mt.transportMap, key)
			mt.mu.Unlock()
		}
	}()

	wg.Wait()
}

// TestNameRaceCondition verifies the race condition in Name().
func TestNameRaceCondition(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mt := &Transport{
		ctx:          ctx,
		cancel:       cancel,
		transportMap: make(map[transport.SubConnectionID]*reconnect.Transport),
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
			key := transport.SubConnectionID(fmt.Sprintf("temp-%d", i%10))
			delete(mt.transportMap, key)
			mt.mu.Unlock()

			mt.mu.Lock()
			delete(mt.transportMap, key)
			mt.mu.Unlock()
		}
	}()

	wg.Wait()
}
