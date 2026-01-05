package multi

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"sync"
	"time"

	"github.com/aptpod/iscp-go/errors"
	"github.com/aptpod/iscp-go/internal/ch"
	"github.com/aptpod/iscp-go/log"
	"github.com/aptpod/iscp-go/transport"
	"github.com/aptpod/iscp-go/transport/reconnect"
)

// MultiOverallStatus は multi.Transport 全体の状態を表します。
type MultiOverallStatus int

const (
	// MultiOverallStatusAllConnected は全ての内部トランスポートが接続されている状態です。
	MultiOverallStatusAllConnected MultiOverallStatus = iota
	// MultiOverallStatusPartiallyConnected は一部の内部トランスポートが接続されている状態です。
	MultiOverallStatusPartiallyConnected
	// MultiOverallStatusAllReconnecting は接続済みのトランスポートがなく、全てが再接続中または切断状態（うち少なくとも1つは再接続中）の状態です。
	MultiOverallStatusAllReconnecting
	// MultiOverallStatusDisconnected は全ての内部トランスポートが切断状態、またはトランスポートが存在しない状態です。
	MultiOverallStatusDisconnected
)

func (s MultiOverallStatus) String() string {
	switch s {
	case MultiOverallStatusAllConnected:
		return "AllConnected"
	case MultiOverallStatusPartiallyConnected:
		return "PartiallyConnected"
	case MultiOverallStatusAllReconnecting:
		return "AllReconnecting"
	case MultiOverallStatusDisconnected:
		return "Disconnected"
	default:
		return fmt.Sprintf("UnknownStatus(%d)", s)
	}
}

var _ transport.Transport = (*Transport)(nil)

type readRes struct {
	bs  []byte
	err error
}

type Transport struct {
	// Context management
	ctx    context.Context
	cancel context.CancelFunc

	// Channel management
	readResCh chan *readRes

	// Synchronization
	mu         sync.RWMutex
	readLoopWg sync.WaitGroup

	// Transport management
	transportMap      map[transport.TransportID]*reconnect.Transport
	transportSelector TransportSelector

	// ECF selector support (optional)
	ecfUpdater              ECFTransportUpdater
	ecfMetricsUpdateEnabled bool

	// Overall status management
	overallStatus       MultiOverallStatus
	overallStatusMu     sync.RWMutex
	statusCheckInterval time.Duration
	statusCheckTicker   *time.Ticker

	// Logging
	logger log.Logger
}

// TransportMap は TransportID と StatusAwareTransport のマップです。
type TransportMap map[transport.TransportID]*reconnect.Transport

func (t TransportMap) TransportIDs() []transport.TransportID {
	res := make([]transport.TransportID, 0, len(t))
	for id := range t {
		res = append(res, id)
	}
	return res
}

type TransportConfig struct {
	TransportMap      TransportMap
	TransportSelector TransportSelector
	Logger            log.Logger
	// StatusCheckInterval は、内部トランスポートの状態を定期的に確認する間隔です。
	// 0以下の場合は、デフォルト値（例: 5秒）が使用されます。
	StatusCheckInterval time.Duration
}

const (
	defaultStatusCheckInterval = 5 * time.Second
)

func NewTransport(c TransportConfig) (*Transport, error) {
	if err := validateConfig(&c); err != nil {
		return nil, err
	}

	m := &Transport{
		readResCh:           make(chan *readRes, 1024),
		transportMap:        make(map[transport.TransportID]*reconnect.Transport),
		transportSelector:   c.TransportSelector,
		logger:              c.Logger,
		statusCheckInterval: c.StatusCheckInterval,
	}
	maps.Copy(m.transportMap, c.TransportMap)

	if m.statusCheckInterval <= 0 {
		m.statusCheckInterval = defaultStatusCheckInterval
	}

	m.ctx, m.cancel = context.WithCancel(context.Background())

	// ECFSelector または MultiTransportSetter をサポートするセレクタの初期化
	if setter, ok := c.TransportSelector.(MultiTransportSetter); ok {
		setter.SetMultiTransport(m)
	}

	// ECFTransportUpdater をサポートするセレクタの場合、メトリクス更新を有効化
	if updater, ok := c.TransportSelector.(ECFTransportUpdater); ok {
		m.ecfUpdater = updater
		m.ecfMetricsUpdateEnabled = true
		// ロガーを設定
		updater.SetLogger(c.Logger)
		// 初回のメトリクス更新
		m.updateECFMetrics()
	}

	go m.readLoop()
	go m.statusMonitorLoop()

	// ECFメトリクス更新が有効な場合、更新ループを開始
	if m.ecfMetricsUpdateEnabled {
		go m.ecfMetricsUpdateLoop()
	}

	return m, nil
}

func validateConfig(c *TransportConfig) error {
	if c.Logger == nil {
		c.Logger = log.NewNop()
	}

	if len(c.TransportMap) == 0 {
		return errors.New("transport map cannot be empty")
	}
	for _, t := range c.TransportMap {
		if t.NegotiationParams().TransportGroupID == "" {
			return errors.New("transport group ID cannot be empty")
		}
	}

	if c.TransportSelector == nil {
		return errors.New("transport selector cannot be nil")
	}

	return nil
}

func (m *Transport) readLoop() {
	m.logger.Infof(m.ctx, "Starting read loop")
	defer m.logger.Infof(m.ctx, "Stopping read loop")

	// マップのスナップショットを取得してイテレーションする
	// これにより、readLoopTransport の defer による delete との競合を回避
	m.mu.RLock()
	transports := make(map[transport.TransportID]*reconnect.Transport, len(m.transportMap))
	for tID, t := range m.transportMap {
		transports[tID] = t
	}
	m.mu.RUnlock()

	for tID, t := range transports {
		// WaitGroup.Add は goroutine 開始前に呼ぶ（Wait との競合を回避）
		m.readLoopWg.Add(1)
		go m.readLoopTransport(tID, t)
	}

	// Wait for the context to be done, then wait for all read loops to finish.
	<-m.ctx.Done()
	m.readLoopWg.Wait()
}

// statusMonitorLoop は定期的に内部トランスポートの状態を監視し、
// multi.Transport 全体の状態を更新します。
func (m *Transport) statusMonitorLoop() {
	m.logger.Infof(m.ctx, "Starting status monitor loop with interval %v", m.statusCheckInterval)
	defer m.logger.Infof(m.ctx, "Stopping status monitor loop")

	m.statusCheckTicker = time.NewTicker(m.statusCheckInterval)
	defer m.statusCheckTicker.Stop()

	// 初回チェック
	m.updateOverallStatus()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-m.statusCheckTicker.C:
			m.updateOverallStatus()
		}
	}
}

// updateOverallStatus は現在の内部トランスポートの状態から multi.Transport 全体の状態を計算し更新します。
// 必要であれば multi.Transport を終了させます。
func (m *Transport) updateOverallStatus() {
	m.mu.RLock()
	if len(m.transportMap) == 0 {
		m.mu.RUnlock()
		m.setOverallStatus(MultiOverallStatusDisconnected)
		m.logger.Infof(m.ctx, "All transports removed or closed, multi-transport is now Disconnected. Shutting down.")
		m.cancel() // トランスポートが0になったら終了
		return
	}

	var (
		connectedCount    int
		reconnectingCount int
		disconnectedCount int
		totalCount        = len(m.transportMap)
	)

	for tID, tr := range m.transportMap {
		status := tr.Status() // StatusProviderの実装が前提

		switch status {
		case reconnect.StatusConnected:
			connectedCount++
		case reconnect.StatusReconnecting, reconnect.StatusConnecting:
			reconnectingCount++
		case reconnect.StatusDisconnected:
			disconnectedCount++
		default:
			// 未知のステータスはDisconnectedとして扱うか、エラーログを出すなど検討
			m.logger.Warnf(m.ctx, "Unknown status %v for transport %s, treating as Disconnected", status, tID)
			disconnectedCount++
		}
	}
	m.mu.RUnlock()

	var newStatus MultiOverallStatus
	if connectedCount == totalCount {
		newStatus = MultiOverallStatusAllConnected
	} else if connectedCount > 0 {
		newStatus = MultiOverallStatusPartiallyConnected
	} else if reconnectingCount > 0 { // connectedCount == 0 は確定
		newStatus = MultiOverallStatusAllReconnecting
	} else { // connectedCount == 0 && reconnectingCount == 0
		// この時点で残りは全て StatusDisconnected のはず
		newStatus = MultiOverallStatusDisconnected
	}

	m.setOverallStatus(newStatus)

	if newStatus == MultiOverallStatusDisconnected && totalCount > 0 { // トランスポートが0の場合は既にcancel済み
		m.logger.Infof(m.ctx, "Overall status is Disconnected. Shutting down multi-transport.")
		m.cancel() // Disconnected状態になったら終了
	}
}

func (m *Transport) setOverallStatus(status MultiOverallStatus) {
	m.overallStatusMu.Lock()
	defer m.overallStatusMu.Unlock()
	if m.overallStatus != status {
		m.logger.Infof(m.ctx, "Overall status changed from %s to %s", m.overallStatus, status)
		m.overallStatus = status
	}
}

// OverallStatus は multi.Transport 全体の現在の状態を返します。
func (m *Transport) OverallStatus() MultiOverallStatus {
	m.overallStatusMu.RLock()
	defer m.overallStatusMu.RUnlock()
	return m.overallStatus
}

// AsUnreliable implements Transport.
func (m *Transport) AsUnreliable() (tr transport.UnreliableTransport, ok bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	// 最初の利用可能なTransportを返す
	for _, currentTr := range m.transportMap {
		if unreliable, ok := currentTr.AsUnreliable(); ok {
			return unreliable, true
		}
	}
	return nil, false
}

// Close implements Transport.
func (m *Transport) Close() error {
	return m.CloseWithStatus(transport.CloseStatusNormal)
}

// Transports は内部のトランスポートマップを返します。
// 各トランスポートのメトリクスを取得する際に使用します。
func (m *Transport) Transports() TransportMap {
	m.mu.RLock()
	defer m.mu.RUnlock()
	// コピーを返してスレッドセーフにする
	result := make(TransportMap, len(m.transportMap))
	for k, v := range m.transportMap {
		result[k] = v
	}
	return result
}

// Close implements Transport.
func (m *Transport) CloseWithStatus(status transport.CloseStatus) error {
	m.cancel()

	var transportsToClose []*reconnect.Transport
	m.mu.RLock()
	for _, v := range m.transportMap {
		transportsToClose = append(transportsToClose, v)
	}
	m.mu.RUnlock()

	var errs error
	for _, v := range transportsToClose {
		errs = errors.Join(errs, v.CloseWithStatus(status))
	}
	return errs
}

// Name implements Transport.
func (m *Transport) Name() transport.Name {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.transportMap))
	for id, t := range m.transportMap {
		names = append(names, fmt.Sprintf("%s-%s", id, t.Name()))
	}
	return transport.Name("multiple-" + strings.Join(names, "-"))
}

// NegotiationParams implements Transport.
func (m *Transport) NegotiationParams() transport.NegotiationParams {
	m.mu.RLock()
	defer m.mu.RUnlock()
	// 最初の利用可能なTransportのNegotiationParamsを返す
	for _, tr := range m.transportMap {
		return tr.NegotiationParams()
	}
	// トランスポートがない場合は空のNegotiationParamsを返す
	return transport.NegotiationParams{}
}

// Read implements Transport.
func (m *Transport) Read() ([]byte, error) {
	res, ok := ch.ReadOrDoneOne(m.ctx, m.readResCh)
	if !ok {
		return nil, transport.ErrAlreadyClosed
	}

	if res.err != nil {
		return nil, res.err
	}
	return res.bs, nil
}

// RxBytesCounterValue implements Transport.
func (m *Transport) RxBytesCounterValue() uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var res uint64
	for _, t := range m.transportMap {
		res += t.RxBytesCounterValue()
	}
	return res
}

// TxBytesCounterValue implements Transport.
func (m *Transport) TxBytesCounterValue() uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var res uint64
	for _, t := range m.transportMap {
		res += t.TxBytesCounterValue()
	}
	return res
}

// Write implements Transport.
//
// 注意: 現在の実装は同期的であり、queueSize（送信待ちキューサイズ）は常に0として扱われます。
// 将来的に非同期送信（WriteAsync）をサポートする場合は、以下の対応が必要です:
//   - Transport構造体にqueueSizeフィールド（uint64, atomic操作用）を追加
//   - 送信前: atomic.AddUint64(&m.queueSize, uint64(len(bs)))
//   - 送信後: atomic.AddUint64(&m.queueSize, ^uint64(len(bs)-1)) // 減算
//   - ECFUpdater.SetQueueSize() の呼び出し
//
// これにより、ECFアルゴリズムの不等式（x_f, x_s）で送信待ちデータ量を考慮できます。
func (m *Transport) Write(bs []byte) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// データサイズに基づいて最適なTransportIDを選択
	// セレクターが接続状態を考慮してフォールバック済みのIDを返す
	selectedID := m.transportSelector.Get(int64(len(bs)))
	if selectedID == "" {
		return transport.ErrAlreadyClosed
	}

	return m.transportMap[selectedID].Write(bs)
}

func (m *Transport) readLoopTransport(tID transport.TransportID, t *reconnect.Transport) {
	// readLoopWg.Add(1) は readLoop() で goroutine 開始前に呼ばれる
	defer m.readLoopWg.Done()

	m.logger.Infof(m.ctx, "Starting read loop for transport %s", tID)
	defer m.logger.Infof(m.ctx, "Stopping read loop for transport %s", tID)

	defer func() {
		m.mu.Lock()
		delete(m.transportMap, tID)
		m.mu.Unlock()
	}()

	for {
		select {
		case <-m.ctx.Done():
			return
		default:
		}

		res, err := t.Read()
		if err != nil {
			if transport.IsNormalClose(err) {
				m.logger.Infof(m.ctx, "Transport %s closed normally (will exit read loop)", tID)
			} else {
				m.logger.Warnf(m.ctx, "Error reading from transport %s: %v (will exit read loop)", tID, err)
			}
			return
		}

		ch.WriteOrDone(m.ctx, &readRes{bs: res, err: nil}, m.readResCh)
	}
}

// ecfMetricsUpdateInterval は ECF メトリクス更新の間隔です。
const ecfMetricsUpdateInterval = 100 * time.Millisecond

// ecfMetricsUpdateLoop は ECF セレクタ用のメトリクス更新を定期的に実行します。
func (m *Transport) ecfMetricsUpdateLoop() {
	m.logger.Infof(m.ctx, "Starting ECF metrics update loop with interval %v", ecfMetricsUpdateInterval)
	defer m.logger.Infof(m.ctx, "Stopping ECF metrics update loop")

	ticker := time.NewTicker(ecfMetricsUpdateInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.updateECFMetrics()
		}
	}
}

// updateECFMetrics は各トランスポートのメトリクスを取得し、ECFセレクタに更新します。
func (m *Transport) updateECFMetrics() {
	if m.ecfUpdater == nil {
		return
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	for tID, tr := range m.transportMap {
		// reconnect.Transport から MetricsProvider を取得
		// reconnect.Transport が MetricsProvider インターフェースを実装していることを確認
		info := NewTransportInfo(tID, tr)
		m.ecfUpdater.UpdateTransport(tID, info)
	}
}
