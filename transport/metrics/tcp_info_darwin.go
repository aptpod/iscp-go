//go:build darwin

package metrics

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const (
	// メトリクスがまだ利用できない場合のデフォルト値
	defaultRTT    = 100 * time.Millisecond
	defaultRTTVar = 50 * time.Millisecond
	defaultCWND   = 14600 // 10 * MSS (1460 バイト)
)

var _ ManagedMetricsProvider = (*TCPInfoProvider)(nil)

// TCPInfoProvider は、TCP_CONNECTION_INFO syscall を介してカーネルからトランスポートメトリクスを取得します。
// バックグラウンドで定期的にメトリクスを更新し、スレッドセーフなアクセスを提供します。
//
// この実装は Darwin (macOS) 専用であり、TCP 接続が必要です。
type TCPInfoProvider struct {
	conn net.Conn

	// バックグラウンド更新の制御
	stateMu  sync.Mutex
	started  bool
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	interval time.Duration

	// カーネルからのメトリクス（metricsMu で保護）
	metricsMu     sync.RWMutex
	smoothedRTT   time.Duration
	rttvar        time.Duration
	cwnd          uint64
	bytesInFlight uint64 // 送信バッファのバイト数
}

// NewTCPInfoProvider は、新しい TCPInfoProvider を作成します。
//
// conn: 監視するネットワーク接続（*net.TCPConn である必要があります）
// interval: バックグラウンドメトリクス収集の更新間隔（例: 100ms）
//
// プロバイダーは、メトリクス収集を開始するために Start() で開始する必要があります。
func NewTCPInfoProvider(conn net.Conn, interval time.Duration) *TCPInfoProvider {
	ctx, cancel := context.WithCancel(context.Background())
	return &TCPInfoProvider{
		conn:     conn,
		interval: interval,
		ctx:      ctx,
		cancel:   cancel,
	}
}

// Start は、バックグラウンドメトリクス収集ループを開始します。
// これは、プロバイダーを作成した後に一度呼び出す必要があります。
// すでに開始されているか、すでに停止している場合はエラーを返します。
func (p *TCPInfoProvider) Start() error {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()

	if p.ctx.Err() != nil {
		return fmt.Errorf("TCPInfoProvider already stopped, cannot restart")
	}
	if p.started {
		return fmt.Errorf("TCPInfoProvider already started")
	}

	p.started = true
	p.wg.Add(1)
	go p.updateLoop()
	return nil
}

// updateLoop は、定期的に update() を呼び出してカーネルからメトリクスを更新します。
func (p *TCPInfoProvider) updateLoop() {
	defer p.wg.Done()
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			_ = p.update()
		case <-p.ctx.Done():
			return
		}
	}
}

// update は、getsockopt syscall を介してカーネルから TCP_CONNECTION_INFO を取得します。
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
		info, err2 := unix.GetsockoptTCPConnectionInfo(int(fd), unix.IPPROTO_TCP, unix.TCP_CONNECTION_INFO)
		if err2 != nil {
			sysErr = err2
			return
		}

		p.metricsMu.Lock()
		defer p.metricsMu.Unlock()

		// Darwin では RTT と RTTVar はマイクロ秒単位
		p.smoothedRTT = time.Duration(info.Srtt) * time.Microsecond
		p.rttvar = time.Duration(info.Rttvar) * time.Microsecond

		// 輻輳ウィンドウ（バイト単位）: cwnd * maxseg
		p.cwnd = uint64(info.Snd_cwnd) * uint64(info.Maxseg)

		// 送信バッファのバイト数を BytesInFlight として使用
		p.bytesInFlight = uint64(info.Snd_sbbytes)
	})
	if err != nil {
		return err
	}
	return sysErr
}

// RTT は、カーネルから平滑化 RTT (SRTT) を返します。
// まだ測定されていない場合は defaultRTT (100ms) を返します。
func (p *TCPInfoProvider) RTT() time.Duration {
	p.metricsMu.RLock()
	defer p.metricsMu.RUnlock()
	if p.smoothedRTT == 0 {
		return defaultRTT
	}
	return p.smoothedRTT
}

// RTTVar は、カーネルから RTT 変動 (RTTVAR、平均偏差) を返します。
// まだ測定されていない場合は defaultRTTVar (50ms) を返します。
func (p *TCPInfoProvider) RTTVar() time.Duration {
	p.metricsMu.RLock()
	defer p.metricsMu.RUnlock()
	if p.rttvar == 0 {
		return defaultRTTVar
	}
	return p.rttvar
}

// CongestionWindow は、輻輳ウィンドウサイズをバイト単位で返します (Snd_cwnd * maxseg)。
// まだ測定されていない場合は defaultCWND (14600 バイト) を返します。
func (p *TCPInfoProvider) CongestionWindow() uint64 {
	p.metricsMu.RLock()
	defer p.metricsMu.RUnlock()
	if p.cwnd == 0 {
		return defaultCWND
	}
	return p.cwnd
}

// BytesInFlight は、現在送信中のバイト数を返します。
// この値は、TCP_CONNECTION_INFO を介してカーネルから取得されます (送信バッファのバイト数)。
func (p *TCPInfoProvider) BytesInFlight() uint64 {
	p.metricsMu.RLock()
	defer p.metricsMu.RUnlock()
	return p.bytesInFlight
}

// Stop は、バックグラウンド更新ループを終了し、完了するまで待機します。
// Stop の複数回呼び出しは安全です（冪等です）。
func (p *TCPInfoProvider) Stop() {
	p.stateMu.Lock()
	cancel := p.cancel
	p.stateMu.Unlock()

	if cancel != nil {
		cancel()
	}
	p.wg.Wait()
}
