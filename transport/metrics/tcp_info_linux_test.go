//go:build linux

package metrics_test

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/aptpod/iscp-go/transport/metrics"
)

func TestTCPInfoProvider_BytesInFlightManagement(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	provider := metrics.NewTCPInfoProvider(client, 100*time.Millisecond)

	tests := []struct {
		name      string
		operation func()
		want      uint64
	}{
		{
			name:      "success: initial state is zero",
			operation: func() {},
			want:      0,
		},
		{
			name:      "success: add 1000 bytes",
			operation: func() { provider.AddBytesInFlight(1000) },
			want:      1000,
		},
		{
			name:      "success: add another 500 bytes",
			operation: func() { provider.AddBytesInFlight(500) },
			want:      1500,
		},
		{
			name:      "success: subtract 300 bytes",
			operation: func() { provider.SubBytesInFlight(300) },
			want:      1200,
		},
		{
			name:      "edge case: underflow protection when subtracting more than available",
			operation: func() { provider.SubBytesInFlight(2000) },
			want:      0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.operation()
			got := provider.BytesInFlight()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestTCPInfoProvider_DefaultValues(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	provider := metrics.NewTCPInfoProvider(client, 100*time.Millisecond)

	tests := []struct {
		name   string
		getter func() any
		want   any
	}{
		{
			name:   "success: default RTT is 100ms",
			getter: func() any { return provider.RTT() },
			want:   100 * time.Millisecond,
		},
		{
			name:   "success: default RTTVar is 50ms",
			getter: func() any { return provider.RTTVar() },
			want:   50 * time.Millisecond,
		},
		{
			name:   "success: default CongestionWindow is 14600 bytes",
			getter: func() any { return provider.CongestionWindow() },
			want:   uint64(14600),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.getter()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestTCPInfoProvider_ConcurrentAccess(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	provider := metrics.NewTCPInfoProvider(client, 50*time.Millisecond)
	err := provider.Start()
	assert.NoError(t, err)
	defer provider.Stop()

	var wg sync.WaitGroup
	numGoroutines := 10
	iterations := 100

	t.Run("success: concurrent reads and writes without race conditions", func(t *testing.T) {
		// Concurrent readers
		for range numGoroutines {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range iterations {
					_ = provider.RTT()
					_ = provider.RTTVar()
					_ = provider.CongestionWindow()
					_ = provider.BytesInFlight()
				}
			}()
		}

		// Concurrent writers
		for range numGoroutines {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range iterations {
					provider.AddBytesInFlight(10)
					provider.SubBytesInFlight(5)
				}
			}()
		}

		wg.Wait()
		// If we reach here without panic or race detector errors, the test passes
	})
}

func TestTCPInfoProvider_StartStop(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	provider := metrics.NewTCPInfoProvider(client, 50*time.Millisecond)

	t.Run("success: start and stop completes without hanging", func(t *testing.T) {
		err := provider.Start()
		assert.NoError(t, err)

		// Let it run for a short time
		time.Sleep(150 * time.Millisecond)

		// Stop should complete without hanging
		done := make(chan struct{})
		go func() {
			provider.Stop()
			close(done)
		}()

		select {
		case <-done:
			// Success
		case <-time.After(2 * time.Second):
			t.Fatal("Stop() did not complete within timeout")
		}
	})

	t.Run("success: values remain accessible and preserved after stop", func(t *testing.T) {
		// Set BytesInFlight before checking (application-layer value)
		provider.AddBytesInFlight(5000)

		// Values should be accessible after stop
		rtt := provider.RTT()
		rttvar := provider.RTTVar()
		cwnd := provider.CongestionWindow()
		bytesInFlight := provider.BytesInFlight()

		// Verify default values are returned (since net.Pipe doesn't provide real TCP_INFO)
		assert.Equal(t, 100*time.Millisecond, rtt, "RTT should be default value after stop")
		assert.Equal(t, 50*time.Millisecond, rttvar, "RTTVar should be default value after stop")
		assert.Equal(t, uint64(14600), cwnd, "CWND should be default value after stop")

		// Verify application-layer value is preserved after stop
		assert.Equal(t, uint64(5000), bytesInFlight, "BytesInFlight should preserve value after stop")
	})
}

func TestTCPInfoProvider_StartErrors(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	provider := metrics.NewTCPInfoProvider(client, 50*time.Millisecond)

	t.Run("error: start called twice", func(t *testing.T) {
		err := provider.Start()
		assert.NoError(t, err)

		// Second call should return error
		err = provider.Start()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "already started")
	})

	t.Run("error: start after stop", func(t *testing.T) {
		provider.Stop()

		// Start after stop should return error
		err := provider.Start()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "already stopped")
	})
}

func TestTCPInfoProvider_StopIdempotent(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	provider := metrics.NewTCPInfoProvider(client, 50*time.Millisecond)
	err := provider.Start()
	assert.NoError(t, err)

	t.Run("success: multiple stop calls are safe", func(t *testing.T) {
		// First stop
		provider.Stop()

		// Second stop should not panic or hang
		provider.Stop()
	})
}

func TestTCPInfoProvider_NonTCPConnection(t *testing.T) {
	// Use net.Pipe which creates a net.Conn but not a *net.TCPConn
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	provider := metrics.NewTCPInfoProvider(client, 100*time.Millisecond)

	t.Run("success: default values returned for non-TCP connection", func(t *testing.T) {
		// Default values should still be returned even for non-TCP connections
		assert.Equal(t, 100*time.Millisecond, provider.RTT())
		assert.Equal(t, 50*time.Millisecond, provider.RTTVar())
		assert.Equal(t, uint64(14600), provider.CongestionWindow())
	})
}

// Note: Testing actual TCP_INFO retrieval requires a real TCP connection,
// which is better suited for integration tests rather than unit tests.
// The above tests cover the concurrency safety and basic functionality.
