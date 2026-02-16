package multi

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aptpod/iscp-go/log"
	"github.com/aptpod/iscp-go/transport"
	"github.com/aptpod/iscp-go/transport/reconnect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestByteBalancedSelector_Distribution_Direct verifies the byte-balanced selector
// distributes writes correctly using a directly-constructed multi.Transport (no readLoop).
func TestByteBalancedSelector_Distribution_Direct(t *testing.T) {
	pipes, transports := setupTwoTransports(t)
	defer cleanupTransports(transports, pipes)

	selector := NewByteBalancedSelector([]transport.TransportID{"trA", "trB"})

	mt := newDirectMultiTransport(t, transports, selector)
	defer mt.Close()

	countA, countB := writeAndCount(t, mt, pipes, 100, []byte("test-payload-0123456789"))

	t.Logf("Distribution: trA=%d, trB=%d", countA, countB)
	assertBalanced(t, countA, countB, 100, 40)
}

// TestByteBalancedSelector_Distribution_NewTransport verifies byte-balanced distribution
// using multi.NewTransport() which starts readLoop and statusMonitorLoop goroutines.
// This tests for potential RLock reentrance deadlocks between Write() and readLoop.
func TestByteBalancedSelector_Distribution_NewTransport(t *testing.T) {
	pipes, transports := setupTwoTransports(t)
	defer cleanupTransports(transports, pipes)

	selector := NewByteBalancedSelector([]transport.TransportID{"trA", "trB"})

	mt, err := NewTransport(TransportConfig{
		TransportMap: TransportMap{
			"trA": transports.trA,
			"trB": transports.trB,
		},
		TransportSelector: selector,
		Logger:            log.NewNop(),
	})
	require.NoError(t, err)
	defer mt.Close()

	countA, countB := writeAndCount(t, mt, pipes, 100, []byte("test-payload-0123456789"))

	t.Logf("Distribution: trA=%d, trB=%d", countA, countB)
	assertBalanced(t, countA, countB, 100, 40)
}

// TestByteBalancedSelector_Distribution_VaryingSizes verifies distribution with
// varying message sizes (which exercises the "byte count" vs "message count" distinction).
func TestByteBalancedSelector_Distribution_VaryingSizes(t *testing.T) {
	pipes, transports := setupTwoTransports(t)
	defer cleanupTransports(transports, pipes)

	selector := NewByteBalancedSelector([]transport.TransportID{"trA", "trB"})

	mt := newDirectMultiTransport(t, transports, selector)
	defer mt.Close()

	// Start readers
	var countA, countB atomic.Int64
	var bytesA, bytesB atomic.Int64
	readerDone := make(chan struct{}, 2)

	startReader := func(pipe transport.ReadWriter, count, bytes *atomic.Int64) {
		go func() {
			defer func() { readerDone <- struct{}{} }()
			for {
				bs, err := pipe.Read()
				if err != nil {
					return
				}
				count.Add(1)
				bytes.Add(int64(len(bs)))
			}
		}()
	}

	startReader(pipes.serverA, &countA, &bytesA)
	startReader(pipes.serverB, &countB, &bytesB)

	// Write messages with varying sizes
	messages := make([][]byte, 100)
	for i := range messages {
		size := 10 + (i%10)*100 // 10, 110, 210, ..., 910, 10, 110, ...
		messages[i] = make([]byte, size)
		for j := range messages[i] {
			messages[i][j] = byte(i)
		}
	}

	for i, msg := range messages {
		err := mt.Write(msg)
		require.NoError(t, err, "write %d should succeed", i)
	}

	// Cleanup
	pipes.serverA.Close()
	pipes.serverB.Close()
	<-readerDone
	<-readerDone

	a := countA.Load()
	b := countB.Load()
	ba := bytesA.Load()
	bb := bytesB.Load()

	t.Logf("Messages: trA=%d, trB=%d", a, b)
	t.Logf("Bytes:    trA=%d, trB=%d", ba, bb)

	// With varying sizes, message counts may differ, but byte counts should be roughly balanced
	totalBytes := ba + bb
	assert.Greater(t, totalBytes, int64(0), "should have written some bytes")

	// Byte balance: each should get at least 35% of total bytes
	minBytesExpected := totalBytes * 35 / 100
	assert.GreaterOrEqual(t, ba, minBytesExpected,
		"trA bytes should be at least 35%% of total (got %d/%d)", ba, totalBytes)
	assert.GreaterOrEqual(t, bb, minBytesExpected,
		"trB bytes should be at least 35%% of total (got %d/%d)", bb, totalBytes)
}

// TestByteBalancedSelector_Distribution_AfterReconnect verifies that the byte-balanced
// selector distributes writes correctly AFTER one transport reconnects.
//
// Bug scenario:
//  1. Both transports connected, write 50 messages → 25:25 distribution (correct)
//  2. Transport B reconnects → its underlying transport's TxBytesCounterValue resets to 0
//  3. Write 50 more messages → selector sees A=large, B=0 → ALL go to B (incorrect!)
//
// Expected behavior: distribution should remain roughly balanced even after reconnection.
func TestByteBalancedSelector_Distribution_AfterReconnect(t *testing.T) {
	// Create pipe pairs
	pipeA1, pipeA2 := transport.Pipe()
	pipeB1, pipeB2 := transport.Pipe()

	// Track dial count for transport B to provide different pipes on reconnect
	var pipeB1New transport.ReadWriter
	var pipeB2New transport.ReadWriter
	var dialCountB atomic.Int32

	trA, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			return &testPipeTransport{
				ReadWriter:        pipeA1,
				negotiationParams: dc.NegotiationParams(),
			}, nil
		}),
		DialConfig: transport.DialConfig{
			TransportID:      "trA",
			TransportGroupID: "test-group",
		},
		MaxReconnectAttempts: 3,
		ReconnectInterval:    time.Millisecond,
		PingInterval:         time.Hour,
		ReadTimeout:          time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)

	// Create new pipes for reconnection BEFORE creating the transport
	pipeB1New, pipeB2New = transport.Pipe()

	trB, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			n := dialCountB.Add(1)
			if n == 1 {
				return &testPipeTransport{
					ReadWriter:        pipeB1,
					negotiationParams: dc.NegotiationParams(),
				}, nil
			}
			// Reconnection: return a new pipe (fresh TxBytesCounterValue = 0)
			return &testPipeTransport{
				ReadWriter:        pipeB1New,
				negotiationParams: dc.NegotiationParams(),
			}, nil
		}),
		DialConfig: transport.DialConfig{
			TransportID:      "trB",
			TransportGroupID: "test-group",
		},
		MaxReconnectAttempts: 3,
		ReconnectInterval:    10 * time.Millisecond,
		PingInterval:         time.Hour,
		ReadTimeout:          time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		trA.Close()
		trB.Close()
		pipeA2.Close()
		pipeB2.Close()
		pipeB2New.Close()
	})

	// Wait for both transports to connect
	require.Eventually(t, func() bool {
		return trA.Status() == reconnect.StatusConnected
	}, 5*time.Second, 10*time.Millisecond, "trA should connect")
	require.Eventually(t, func() bool {
		return trB.Status() == reconnect.StatusConnected
	}, 5*time.Second, 10*time.Millisecond, "trB should connect")

	selector := NewByteBalancedSelector([]transport.TransportID{"trA", "trB"})

	mt := &Transport{
		transportMap: map[transport.TransportID]*reconnect.Transport{
			"trA": trA,
			"trB": trB,
		},
		transportSelector: selector,
		readResCh:         make(chan *readRes, 1024),
		logger:            log.NewNop(),
	}
	mt.ctx, mt.cancel = withCancel()
	selector.SetMultiTransport(mt)

	// Use a single counter for A across all phases (same pipe throughout)
	var totalCountA atomic.Int64
	readerDoneA := make(chan struct{})
	go func() {
		defer close(readerDoneA)
		for {
			_, err := pipeA2.Read()
			if err != nil {
				return
			}
			totalCountA.Add(1)
		}
	}()

	// Phase 1 reader for B (original pipe)
	var phase1CountB atomic.Int64
	readerDoneB1 := make(chan struct{})
	go func() {
		defer close(readerDoneB1)
		for {
			_, err := pipeB2.Read()
			if err != nil {
				return
			}
			phase1CountB.Add(1)
		}
	}()

	// Phase 1: Write 50 messages (should be ~25:25)
	payload := []byte("test-payload-data-0123456789")
	for i := range 50 {
		err := mt.Write(payload)
		require.NoError(t, err, "phase1 write %d should succeed", i)
	}

	t.Logf("Phase 1: trA=%d, trB=%d", totalCountA.Load(), phase1CountB.Load())
	t.Logf("Phase 1 TxBytes: trA=%d, trB=%d",
		trA.TxBytesCounterValue(), trB.TxBytesCounterValue())

	// Trigger reconnection on transport B by closing the server-side pipe
	pipeB2.Close()
	<-readerDoneB1 // Wait for phase 1 B reader to finish

	// Wait for B to reconnect with new pipe
	require.Eventually(t, func() bool {
		return dialCountB.Load() >= 2
	}, 5*time.Second, 10*time.Millisecond, "trB should reconnect")
	require.Eventually(t, func() bool {
		return trB.Status() == reconnect.StatusConnected
	}, 5*time.Second, 10*time.Millisecond, "trB should reconnect successfully")

	t.Logf("After reconnect TxBytes: trA=%d, trB=%d",
		trA.TxBytesCounterValue(), trB.TxBytesCounterValue())

	// Phase 2 reader for B (new pipe)
	var phase2CountB atomic.Int64
	readerDoneB2 := make(chan struct{})
	go func() {
		defer close(readerDoneB2)
		for {
			_, err := pipeB2New.Read()
			if err != nil {
				return
			}
			phase2CountB.Add(1)
		}
	}()

	// Record A count before phase 2
	aBeforePhase2 := totalCountA.Load()

	// Phase 2: Write 50 more messages (after B reconnected with counter=0)
	for i := range 50 {
		err := mt.Write(payload)
		require.NoError(t, err, "phase2 write %d should succeed", i)
	}

	// Stop all readers
	pipeA2.Close()
	pipeB2New.Close()
	<-readerDoneA
	<-readerDoneB2

	// Calculate phase 2 counts
	phase2A := totalCountA.Load() - aBeforePhase2
	phase2B := phase2CountB.Load()
	total2 := phase2A + phase2B

	t.Logf("Phase 2 distribution: trA=%d, trB=%d, total=%d", phase2A, phase2B, total2)

	// Phase 2 should still be balanced: each transport should get at least 35%
	// Without the bug, 50 messages → ~25:25. With the bug (counter reset),
	// B gets ~37 messages (74%) because its counter reset to 0 and the selector
	// sends all traffic there until B catches up to A's accumulated counter.
	assert.Equal(t, int64(50), total2, "all phase 2 messages should be accounted for")

	minExpected := int64(50 * 35 / 100) // 35% of 50 = 17
	assert.GreaterOrEqual(t, phase2A, minExpected,
		"trA should receive at least 35%% in phase 2 (got %d/%d), counter reset caused bias", phase2A, total2)
	assert.GreaterOrEqual(t, phase2B, minExpected,
		"trB should receive at least 35%% in phase 2 (got %d/%d), counter reset caused bias", phase2B, total2)
}

// --- Test helpers ---

type testPipes struct {
	clientA, serverA transport.ReadWriter
	clientB, serverB transport.ReadWriter
}

type testTransports struct {
	trA, trB *reconnect.Transport
}

func setupTwoTransports(t *testing.T) (testPipes, testTransports) {
	t.Helper()

	pipeA1, pipeA2 := transport.Pipe()
	pipeB1, pipeB2 := transport.Pipe()

	trA, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			return &testPipeTransport{
				ReadWriter:        pipeA1,
				negotiationParams: dc.NegotiationParams(),
			}, nil
		}),
		DialConfig: transport.DialConfig{
			TransportID:      "trA",
			TransportGroupID: "test-group",
		},
		MaxReconnectAttempts: 1,
		ReconnectInterval:    time.Millisecond,
		PingInterval:         time.Hour,
		ReadTimeout:          time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)

	trB, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			return &testPipeTransport{
				ReadWriter:        pipeB1,
				negotiationParams: dc.NegotiationParams(),
			}, nil
		}),
		DialConfig: transport.DialConfig{
			TransportID:      "trB",
			TransportGroupID: "test-group",
		},
		MaxReconnectAttempts: 1,
		ReconnectInterval:    time.Millisecond,
		PingInterval:         time.Hour,
		ReadTimeout:          time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return trA.Status() == reconnect.StatusConnected
	}, 5*time.Second, 10*time.Millisecond, "trA should connect")
	require.Eventually(t, func() bool {
		return trB.Status() == reconnect.StatusConnected
	}, 5*time.Second, 10*time.Millisecond, "trB should connect")

	return testPipes{
			clientA: pipeA1, serverA: pipeA2,
			clientB: pipeB1, serverB: pipeB2,
		}, testTransports{
			trA: trA, trB: trB,
		}
}

func cleanupTransports(tr testTransports, pipes testPipes) {
	tr.trA.Close()
	tr.trB.Close()
	pipes.serverA.Close()
	pipes.serverB.Close()
}

func newDirectMultiTransport(t *testing.T, tr testTransports, selector TransportSelector) *Transport {
	t.Helper()

	mt := &Transport{
		transportMap: map[transport.TransportID]*reconnect.Transport{
			"trA": tr.trA,
			"trB": tr.trB,
		},
		transportSelector: selector,
		readResCh:         make(chan *readRes, 1024),
		logger:            log.NewNop(),
	}
	mt.ctx, mt.cancel = withCancel()

	if setter, ok := selector.(MultiTransportSetter); ok {
		setter.SetMultiTransport(mt)
	}

	return mt
}

func withCancel() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}

func writeAndCount(t *testing.T, mt *Transport, pipes testPipes, numMessages int, payload []byte) (int64, int64) {
	t.Helper()

	var countA, countB atomic.Int64
	readerDone := make(chan struct{}, 2)

	go func() {
		defer func() { readerDone <- struct{}{} }()
		for {
			_, err := pipes.serverA.Read()
			if err != nil {
				return
			}
			countA.Add(1)
		}
	}()

	go func() {
		defer func() { readerDone <- struct{}{} }()
		for {
			_, err := pipes.serverB.Read()
			if err != nil {
				return
			}
			countB.Add(1)
		}
	}()

	for i := range numMessages {
		err := mt.Write(payload)
		require.NoError(t, err, "write %d should succeed", i)
	}

	// Close server-side pipes to stop readers
	pipes.serverA.Close()
	pipes.serverB.Close()
	<-readerDone
	<-readerDone

	return countA.Load(), countB.Load()
}

func assertBalanced(t *testing.T, countA, countB int64, total, minPercent int) {
	t.Helper()

	assert.Equal(t, int64(total), countA+countB, "all messages should be accounted for")

	minExpected := int64(total * minPercent / 100)
	assert.GreaterOrEqual(t, countA, minExpected,
		"trA should receive at least %d%% (%d messages, got %d)", minPercent, minExpected, countA)
	assert.GreaterOrEqual(t, countB, minExpected,
		"trB should receive at least %d%% (%d messages, got %d)", minPercent, minExpected, countB)
}
