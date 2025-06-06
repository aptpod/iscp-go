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

// StatusAwareTransport は、通常のTransport機能に加え、状態提供機能を持つトランスポートです。
type StatusAwareTransport interface {
	transport.Transport
	reconnect.StatusProvider
}

// ErrInvalidSchedulerMode represents an error when an invalid scheduler mode is specified
var ErrInvalidSchedulerMode = errors.New("invalid scheduler mode")

// ErrMissingEventScheduler represents an error when event scheduler configuration is missing
var ErrMissingEventScheduler = errors.New("required EventScheduler and Subscriber")

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
	transportMap          map[transport.TransportID]StatusAwareTransport
	lastReadTransportID   transport.TransportID
	lastReadTransportIDmu sync.RWMutex
	transportIDCh         <-chan transport.TransportID
	currentTransportID    transport.TransportID

	// Overall status management
	overallStatus       MultiOverallStatus
	overallStatusMu     sync.RWMutex
	statusCheckInterval time.Duration
	statusCheckTicker   *time.Ticker

	// Logging
	logger log.Logger
}

type SchedulerMode int

const (
	SchedulerModePolling SchedulerMode = iota
	SchedulerModeEvent
)

// TransportMap は TransportID と StatusAwareTransport のマップです。
type TransportMap map[transport.TransportID]StatusAwareTransport

func (t TransportMap) TransportIDs() []transport.TransportID {
	res := make([]transport.TransportID, 0, len(t))
	for id := range t {
		res = append(res, id)
	}
	return res
}

type TransportConfig struct {
	TransportMap       TransportMap
	InitialTransportID transport.TransportID
	SchedulerMode      SchedulerMode
	PollingScheduler   *PollingScheduler
	EventScheduler     *EventScheduler
	Logger             log.Logger
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
		transportMap:        make(map[transport.TransportID]StatusAwareTransport), // 初期化時にコピー
		currentTransportID:  c.InitialTransportID,
		logger:              c.Logger,
		statusCheckInterval: c.StatusCheckInterval,
	}
	maps.Copy(m.transportMap, c.TransportMap)

	if m.statusCheckInterval <= 0 {
		m.statusCheckInterval = defaultStatusCheckInterval
	}

	m.ctx, m.cancel = context.WithCancel(context.Background())

	if err := m.initializeScheduler(c); err != nil {
		m.cancel()
		return nil, fmt.Errorf("initialize scheduler: %w", err)
	}

	go m.transportIDLoop()
	go m.readLoop()
	go m.statusMonitorLoop() // 状態監視ループを開始

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

	return nil
}

func (m *Transport) initializeScheduler(c TransportConfig) error {
	switch c.SchedulerMode {
	case SchedulerModePolling:
		return m.initPollingScheduler(c)
	case SchedulerModeEvent:
		return m.initEventScheduler(c)
	default:
		return fmt.Errorf("%v: %w", c.SchedulerMode, ErrInvalidSchedulerMode)
	}
}

func (m *Transport) initPollingScheduler(c TransportConfig) error {
	if c.PollingScheduler == nil {
		c.PollingScheduler = &PollingScheduler{
			Poller: &RoundRobinPoller{
				transportIDs: c.TransportMap.TransportIDs(),
			},
			Interval: time.Second * 5,
		}
	}

	if s, ok := c.PollingScheduler.Poller.(MultiTransportSetter); ok {
		s.SetMultiTransport(m)
	}
	m.transportIDCh = c.PollingScheduler.loop(m.ctx)
	return nil
}

func (m *Transport) initEventScheduler(c TransportConfig) error {
	if c.EventScheduler == nil || c.EventScheduler.Subscriber == nil {
		return ErrMissingEventScheduler
	}
	m.transportIDCh = c.EventScheduler.loop(m.ctx)
	return nil
}

func (m *Transport) transportIDLoop() {
	m.logger.Infof(m.ctx, "Starting transport ID loop")
	defer m.logger.Infof(m.ctx, "Stopping transport ID loop")
	for id := range ch.ReadOrDone(m.ctx, m.transportIDCh) {
		m.mu.Lock()
		if m.currentTransportID != id && id != "" {
			m.logger.Infof(m.ctx, "Switching transport to %s", id)
			m.currentTransportID = id
		}

		m.mu.Unlock()
	}
}

func (m *Transport) readLoop() {
	m.logger.Infof(m.ctx, "Starting read loop")
	defer m.logger.Infof(m.ctx, "Stopping read loop")

	for tID, t := range m.transportMap {
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

	for _, tr := range m.transportMap {
		status := tr.Status() // StatusProviderの実装が前提
		switch status {
		case reconnect.StatusConnected:
			connectedCount++
		case reconnect.StatusReconnecting:
			reconnectingCount++
		case reconnect.StatusDisconnected:
			disconnectedCount++
		default:
			// 未知のステータスはDisconnectedとして扱うか、エラーログを出すなど検討
			m.logger.Warnf(m.ctx, "Unknown status %v for transport, treating as Disconnected", status)
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
	currentTr, exists := m.transportMap[m.currentTransportID]
	if !exists {
		return nil, false
	}
	return currentTr.AsUnreliable()
}

// Close implements Transport.
func (m *Transport) Close() error {
	return m.CloseWithStatus(transport.CloseStatusNormal)
}

// Close implements Transport.
func (m *Transport) CloseWithStatus(status transport.CloseStatus) error {
	m.cancel()

	var transportsToClose []StatusAwareTransport
	m.mu.RLock()
	for _, v := range m.transportMap {
		transportsToClose = append(transportsToClose, v)
	}
	m.mu.RUnlock()

	var errs error
	for _, v := range transportsToClose {
		switch vv := v.(type) {
		case transport.Closer:
			errs = errors.Join(errs, vv.CloseWithStatus(status))
		default:
			errs = errors.Join(errs, v.Close())
		}
	}
	return errs
}

// Name implements Transport.
func (m *Transport) Name() transport.Name {
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
	// currentTransportID が削除されている可能性を考慮
	currentTr, exists := m.transportMap[m.currentTransportID]
	if !exists {
		// 適切なデフォルト値またはエラー処理を検討
		// ここでは空のNegotiationParamsを返す例
		return transport.NegotiationParams{}
	}
	return currentTr.NegotiationParams()
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
	var res uint64
	for _, t := range m.transportMap {
		res += t.RxBytesCounterValue()
	}
	return res
}

// TxBytesCounterValue implements Transport.
func (m *Transport) TxBytesCounterValue() uint64 {
	var res uint64
	for _, t := range m.transportMap {
		res += t.TxBytesCounterValue()
	}
	return res
}

// Write implements Transport.
func (m *Transport) Write(bs []byte) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	// close when timeout
	conn, exists := m.transportMap[m.currentTransportID]
	if exists {
		if conn.Status() == reconnect.StatusConnected {
			return conn.Write(bs)
		}
		fallbackTr, fallbackExists := m.fallbackConn()
		if !fallbackExists {
			return transport.ErrAlreadyClosed
		}
		return fallbackTr.Write(bs)
	}
	fallbackTr, fallbackExists := m.fallbackConn()
	if !fallbackExists {
		return transport.ErrAlreadyClosed
	}
	return fallbackTr.Write(bs)
}

func (m *Transport) fallbackConn() (connected StatusAwareTransport, exists bool) {
	var reconnectingTransport StatusAwareTransport
	// Iterate through transports to find a connected or reconnecting one.
	for _, t := range m.transportMap {
		// Priority 1: Return immediately if StatusConnected.
		if t.Status() == reconnect.StatusConnected {
			return t, true
		}
		// Hold the first StatusReconnecting transport found for Priority 2.
		if t.Status() == reconnect.StatusReconnecting && reconnectingTransport == nil {
			reconnectingTransport = t
		}
	}

	// Priority 2: Return the held StatusReconnecting transport if found.
	if reconnectingTransport != nil {
		return reconnectingTransport, true
	}

	// No suitable fallback connection found.
	return nil, false
}

func (m *Transport) AddTransport(trID transport.TransportID, tr StatusAwareTransport) error {
	m.mu.Lock()
	if _, exists := m.transportMap[trID]; exists {
		m.mu.Unlock()
		return fmt.Errorf("transport with ID %s already exists", trID)
	}
	m.transportMap[trID] = tr
	m.mu.Unlock()

	m.logger.Infof(m.ctx, "Added transport %s and starting its read loop", trID)
	go m.readLoopTransport(trID, tr)

	// 新しいトランスポートが追加されたので、状態を即時評価する
	// (次のTickerを待たずに評価するために、手動で呼び出すか、Tickerをリセット)
	// ここでは、次の定期チェックで新しいトランスポートが含められることを期待する。
	// もし即時反映が必要な場合は、m.updateOverallStatus() を直接呼ぶか、
	// m.statusCheckTicker.Reset(time.Nanosecond) のような形で即時発火を促す。
	return nil
}

func (m *Transport) readLoopTransport(tID transport.TransportID, t StatusAwareTransport) {
	m.readLoopWg.Add(1)
	defer m.readLoopWg.Done()

	m.logger.Infof(m.ctx, "Starting read loop for transport %s", tID)
	defer m.logger.Infof(m.ctx, "Stopping read loop for transport %s", tID)

	for {
		select {
		case <-m.ctx.Done():
			return
		default:
		}
		res, err := t.Read()
		if err != nil {
			if errors.Is(err, errors.ErrConnectionClosed) {
				m.logger.Infof(m.ctx, "Transport %s closed (ErrConnectionClosed). Removing from map.", tID)
				m.mu.Lock()
				delete(m.transportMap, tID)
				m.mu.Unlock()
				// 状態監視ループは次のインターバルで検知するが、即時評価を促すことも可能
				// (例: m.statusCheckTicker.Reset(time.Nanosecond) や専用チャンネルで通知)
				// ここではシンプルに次の定期チェックに任せる
				return // ループを終了
			}
			m.logger.Warnf(m.ctx, "Error reading from transport %s: %v", tID, err)
			// Read()の呼び出し元にもエラーを返す
			ch.WriteOrDone(m.ctx, &readRes{bs: nil, err: err}, m.readResCh)
			return // その他のエラーでもループを終了
		}
		m.lastReadTransportIDmu.Lock()
		m.lastReadTransportID = tID
		m.lastReadTransportIDmu.Unlock()
		ch.WriteOrDone(m.ctx, &readRes{bs: res, err: nil}, m.readResCh)
	}
}
