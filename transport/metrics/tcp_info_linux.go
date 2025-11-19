//go:build linux

package metrics

import (
	"fmt"
	"net"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const (
	// Default values when metrics are not yet available
	defaultRTT    = 100 * time.Millisecond
	defaultRTTVar = 50 * time.Millisecond
	defaultCWND   = 14600 // 10 * MSS (1460 bytes)
)

// TCPInfoProvider retrieves transport metrics from the kernel via TCP_INFO syscall.
// It periodically updates metrics in the background and provides thread-safe access.
//
// This implementation is Linux-only and requires a TCP connection.
type TCPInfoProvider struct {
	conn net.Conn

	// Background update control
	stateMu  sync.Mutex
	started  bool
	stopped  bool
	stopCh   chan struct{}
	wg       sync.WaitGroup
	interval time.Duration

	// Metrics from kernel (protected by metricsMu)
	metricsMu   sync.RWMutex
	smoothedRTT time.Duration
	rttvar      time.Duration
	cwnd        uint64

	// Managed at application layer
	bytesInFlight uint64
}

// NewTCPInfoProvider creates a new TCPInfoProvider.
//
// conn: The network connection to monitor (must be *net.TCPConn)
// interval: Update interval for background metrics collection (e.g., 100ms)
//
// The provider must be started with Start() to begin collecting metrics.
func NewTCPInfoProvider(conn net.Conn, interval time.Duration) *TCPInfoProvider {
	return &TCPInfoProvider{
		conn:     conn,
		stopCh:   make(chan struct{}),
		interval: interval,
	}
}

// Start begins the background metrics collection loop.
// This should be called once after creating the provider.
// Returns an error if already started or already stopped.
func (p *TCPInfoProvider) Start() error {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()

	if p.started && !p.stopped {
		return fmt.Errorf("TCPInfoProvider already started")
	}
	if p.stopped {
		return fmt.Errorf("TCPInfoProvider already stopped, cannot restart")
	}

	p.started = true
	p.wg.Add(1)
	go p.updateLoop()
	return nil
}

// updateLoop periodically calls update() to refresh metrics from the kernel.
func (p *TCPInfoProvider) updateLoop() {
	defer p.wg.Done()
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			_ = p.update()
		case <-p.stopCh:
			return
		}
	}
}

// update retrieves TCP_INFO from the kernel via getsockopt syscall.
func (p *TCPInfoProvider) update() error {
	tcpConn, ok := p.conn.(*net.TCPConn)
	if !ok {
		return fmt.Errorf("not a TCP connection")
	}

	raw, err := tcpConn.SyscallConn()
	if err != nil {
		return err
	}

	var sysErr error
	err = raw.Control(func(fd uintptr) {
		ti, err2 := unix.GetsockoptTCPInfo(int(fd), unix.IPPROTO_TCP, unix.TCP_INFO)
		if err2 != nil {
			sysErr = err2
			return
		}

		p.metricsMu.Lock()
		defer p.metricsMu.Unlock()
		p.smoothedRTT = time.Duration(ti.Rtt) * time.Microsecond
		p.rttvar = time.Duration(ti.Rttvar) * time.Microsecond
		p.cwnd = uint64(ti.Snd_cwnd) * uint64(ti.Snd_mss)
	})
	if err != nil {
		return err
	}
	return sysErr
}

// RTT returns the Smoothed RTT (SRTT) from the kernel.
// Returns defaultRTT (100ms) if not yet measured.
func (p *TCPInfoProvider) RTT() time.Duration {
	p.metricsMu.RLock()
	defer p.metricsMu.RUnlock()
	if p.smoothedRTT == 0 {
		return defaultRTT
	}
	return p.smoothedRTT
}

// RTTVar returns the RTT Variation (RTTVAR, Mean Deviation) from the kernel.
// Returns defaultRTTVar (50ms) if not yet measured.
func (p *TCPInfoProvider) RTTVar() time.Duration {
	p.metricsMu.RLock()
	defer p.metricsMu.RUnlock()
	if p.rttvar == 0 {
		return defaultRTTVar
	}
	return p.rttvar
}

// CongestionWindow returns the congestion window size in bytes (Snd_cwnd * Snd_mss).
// Returns defaultCWND (14600 bytes) if not yet measured.
func (p *TCPInfoProvider) CongestionWindow() uint64 {
	p.metricsMu.RLock()
	defer p.metricsMu.RUnlock()
	if p.cwnd == 0 {
		return defaultCWND
	}
	return p.cwnd
}

// BytesInFlight returns the number of bytes currently in flight.
// This is managed at the application layer.
func (p *TCPInfoProvider) BytesInFlight() uint64 {
	p.metricsMu.RLock()
	defer p.metricsMu.RUnlock()
	return p.bytesInFlight
}

// AddBytesInFlight atomically adds n bytes to bytesInFlight.
// Should be called before Write() operations.
func (p *TCPInfoProvider) AddBytesInFlight(n uint64) {
	p.metricsMu.Lock()
	defer p.metricsMu.Unlock()
	p.bytesInFlight += n
}

// SubBytesInFlight atomically subtracts n bytes from bytesInFlight.
// Should be called after Write() operations complete.
func (p *TCPInfoProvider) SubBytesInFlight(n uint64) {
	p.metricsMu.Lock()
	defer p.metricsMu.Unlock()
	if n > p.bytesInFlight {
		p.bytesInFlight = 0
	} else {
		p.bytesInFlight -= n
	}
}

// Stop terminates the background update loop and waits for it to finish.
// Multiple calls to Stop are safe (idempotent).
func (p *TCPInfoProvider) Stop() {
	p.stateMu.Lock()
	if p.stopped {
		p.stateMu.Unlock()
		return
	}
	p.stopped = true
	p.stateMu.Unlock()

	close(p.stopCh)
	p.wg.Wait()
}
