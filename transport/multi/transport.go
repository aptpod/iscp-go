package multi

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"sync"
	"time"

	"github.com/aptpod/iscp-go/v2/errors"
	"github.com/aptpod/iscp-go/v2/internal/ch"
	"github.com/aptpod/iscp-go/v2/log"
	"github.com/aptpod/iscp-go/v2/transport"
	"github.com/aptpod/iscp-go/v2/transport/reconnect"
)

// MultiOverallStatus は multi.Transport 全体の状態を表します。
type MultiOverallStatus int

const (
	// MultiOverallStatusAllConnecting indicates all internal transports are in their initial connection attempt.
	MultiOverallStatusAllConnecting MultiOverallStatus = iota
	// MultiOverallStatusAllConnected は全ての内部トランスポートが接続されている状態です。
	MultiOverallStatusAllConnected
	// MultiOverallStatusPartiallyConnected は一部の内部トランスポートが接続されている状態です。
	MultiOverallStatusPartiallyConnected
	// MultiOverallStatusAllReconnecting は接続済みのトランスポートがなく、全てが再接続中または切断状態（うち少なくとも1つは再接続中）の状態です。
	MultiOverallStatusAllReconnecting
	// MultiOverallStatusDisconnected は全ての内部トランスポートが切断状態、またはトランスポートが存在しない状態です。
	MultiOverallStatusDisconnected
)

func (s MultiOverallStatus) String() string {
	switch s {
	case MultiOverallStatusAllConnecting:
		return "AllConnecting"
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
	//
	// Note: do not add Lock() (writer) on m.mu.
	// writeOnce calls transportSelector.Get while holding m.mu.RLock(), and every selector
	// implementation has a path where Get calls mt.Transports() (which acquires m.mu.RLock() again),
	// so a recursive RLock can occur on the same goroutine. The sync.RWMutex contract
	// forbids recursive RLock: if a writer slips in between, the second RLock
	// blocks and the goroutine deadlocks itself. This currently holds only because
	// production code has no writer for m.mu (transportMap is not modified
	// after construction); adding even one writer could deadlock this path.
	mu         sync.RWMutex
	readLoopWg sync.WaitGroup

	// Transport management
	transportMap      map[transport.SubConnectionID]*reconnect.Transport
	transportSelector TransportSelector

	// Metrics-based selector support (optional)
	metricsUpdater        TransportMetricsUpdater
	metricsUpdaterEnabled bool

	// Overall status management
	overallStatus       MultiOverallStatus
	overallStatusMu     sync.RWMutex
	statusCheckInterval time.Duration
	statusCheckTicker   *time.Ticker
	// noConnected tracks how long there has been no connected sub-connection.
	noConnected *noConnectedTracker
	// teardownOnce makes teardown run only once (shared by the threshold-exceeded path and the all-subs-Disconnected path).
	// It is also consumed at the start of CloseWithStatus, which prevents the teardown path
	// from firing twice (re-entering Close) via an explicit Close.
	teardownOnce sync.Once

	// Logging
	logger log.Logger
}

// TransportMap は SubConnectionID と StatusAwareTransport のマップです。
type TransportMap map[transport.SubConnectionID]*reconnect.Transport

func (t TransportMap) SubConnectionIDs() []transport.SubConnectionID {
	res := make([]transport.SubConnectionID, 0, len(t))
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
	// NoConnectedTransportTimeout は、接続済み（StatusConnected）の sub-connection が
	// 1 本も無い状態がこの時間を超えて継続したときに、multi.Transport 全体を
	// Disconnected にして自身と全 sub-connection を Close するまでの猶予時間です。
	//
	// 0 以下（既定）の場合はこの機能は無効で、全 sub-connection が接続できない
	// 状態が続いても multi.Transport は畳まれません。
	//
	// 従来の reconnect.Transport 向け設定（MaxReconnectAttempts / ReconnectInterval）
	// からこの値を算出するには CalcNoConnectedTransportTimeout を使ってください。
	// multi.Transport を使う構成では各 sub-connection には無期限リトライ（-1）を
	// させ、全体の生死判定をこのフィールドに集約することを想定しています。
	NoConnectedTransportTimeout time.Duration
}

const (
	defaultStatusCheckInterval = 5 * time.Second
	// minStatusCheckInterval is the lower bound when shortening the monitoring interval
	// to match NoConnectedTransportTimeout. It prevents the ticker from firing too frequently with an extremely short threshold.
	minStatusCheckInterval = 10 * time.Millisecond
)

func NewTransport(c TransportConfig) (*Transport, error) {
	if err := validateConfig(&c); err != nil {
		return nil, err
	}

	m := &Transport{
		readResCh:           make(chan *readRes, 1024),
		transportMap:        make(map[transport.SubConnectionID]*reconnect.Transport),
		transportSelector:   c.TransportSelector,
		logger:              c.Logger,
		statusCheckInterval: c.StatusCheckInterval,
		noConnected:         newNoConnectedTracker(c.NoConnectedTransportTimeout),
	}
	maps.Copy(m.transportMap, c.TransportMap)

	if m.statusCheckInterval <= 0 {
		m.statusCheckInterval = defaultStatusCheckInterval
	}
	// The threshold is only evaluated by the level-trigger at each statusCheckInterval,
	// so a threshold shorter than the monitoring interval delays the decision by up to one monitoring interval.
	// When the caller specifies a short threshold, the monitoring interval is shortened automatically.
	if c.NoConnectedTransportTimeout > 0 && c.NoConnectedTransportTimeout < m.statusCheckInterval {
		m.statusCheckInterval = max(c.NoConnectedTransportTimeout/2, minStatusCheckInterval)
	}

	m.ctx, m.cancel = context.WithCancel(context.Background())

	// Initialize a selector that supports ECFSelector or MultiTransportSetter.
	if setter, ok := c.TransportSelector.(MultiTransportSetter); ok {
		setter.SetMultiTransport(m)
	}

	// If the selector supports TransportMetricsUpdater, enable metrics updates.
	if updater, ok := c.TransportSelector.(TransportMetricsUpdater); ok {
		m.metricsUpdater = updater
		m.metricsUpdaterEnabled = true
		// Set the logger.
		updater.SetLogger(c.Logger)
		// Initial metrics update.
		m.updateMetrics()
	}

	// Set a status change callback on each reconnect.Transport
	// and update the overall status in an event-driven way.
	for _, t := range m.transportMap {
		t.SetOnStatusChange(func(oldStatus, newStatus reconnect.Status) {
			m.updateOverallStatus()
		})
	}

	go m.readLoop()
	go m.statusMonitorLoop()

	// If metrics updates are enabled, start the update loop.
	if m.metricsUpdaterEnabled {
		go m.metricsUpdateLoop()
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
		if t.NegotiationParams().SuperConnectionID == "" {
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

	// Take a snapshot of the map and iterate over it.
	// This avoids a race with the delete in the defer of readLoopTransport.
	m.mu.RLock()
	transports := make(map[transport.SubConnectionID]*reconnect.Transport, len(m.transportMap))
	for tID, t := range m.transportMap {
		transports[tID] = t
	}
	m.mu.RUnlock()

	for tID, t := range transports {
		// Call WaitGroup.Add before starting the goroutine (to avoid a race with Wait).
		m.readLoopWg.Add(1)
		go m.readLoopTransport(tID, t)
	}

	// Wait for the context to be done, then wait for all read loops to finish.
	<-m.ctx.Done()
	m.readLoopWg.Wait()
}

// statusMonitorLoop periodically monitors the state of the internal transports
// and updates the overall state of multi.Transport.
func (m *Transport) statusMonitorLoop() {
	m.logger.Infof(m.ctx, "Starting status monitor loop with interval %v", m.statusCheckInterval)
	defer m.logger.Infof(m.ctx, "Stopping status monitor loop")

	m.statusCheckTicker = time.NewTicker(m.statusCheckInterval)
	defer m.statusCheckTicker.Stop()

	// Initial check.
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

// updateOverallStatus calculates and updates the overall state of multi.Transport from the current state of the internal transports.
// It shuts down multi.Transport if necessary.
func (m *Transport) updateOverallStatus() {
	m.mu.RLock()
	if len(m.transportMap) == 0 {
		m.mu.RUnlock()
		m.setOverallStatus(MultiOverallStatusDisconnected)
		m.logger.Infof(m.ctx, "All transports removed or closed, multi-transport is now Disconnected. Shutting down.")
		m.cancel() // Shut down when there are no transports left.
		return
	}

	var (
		connectedCount    int
		connectingCount   int
		reconnectingCount int
		disconnectedCount int
		totalCount        = len(m.transportMap)
	)

	for tID, tr := range m.transportMap {
		status := tr.Status() // Assumes a StatusProvider implementation.

		switch status {
		case reconnect.StatusConnected:
			connectedCount++
		case reconnect.StatusConnecting:
			connectingCount++
		case reconnect.StatusReconnecting:
			reconnectingCount++
		case reconnect.StatusDisconnected:
			disconnectedCount++
		default:
			// Consider treating an unknown status as Disconnected, or logging an error.
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
	} else if connectingCount == totalCount {
		newStatus = MultiOverallStatusAllConnecting
	} else if reconnectingCount > 0 || connectingCount > 0 {
		newStatus = MultiOverallStatusAllReconnecting
	} else { // connectedCount == 0 && reconnectingCount == 0 && connectingCount == 0
		// At this point, all the remaining ones should be StatusDisconnected.
		newStatus = MultiOverallStatusDisconnected
	}

	// If the state of having no connected sub-connection continues beyond the threshold, give up as a whole.
	// AllConnecting (never connected) and AllReconnecting (all disconnected after connecting) are
	// not distinguished. A grace period of the threshold also applies right after startup, so a normal transient state is not falsely detected.
	if m.noConnected.observe(connectedCount > 0) {
		newStatus = MultiOverallStatusDisconnected
		m.setOverallStatus(newStatus)
		// teardown must not be run synchronously here, for two reasons.
		// (1) updateOverallStatus may be called synchronously from a reconnect status callback,
		//     and the caller of the callback holds reconnectMu or r.mu
		//     (transport/reconnect/transport.go:640-652, :767).
		//     Calling sub.CloseWithStatus from within the callback would deadlock itself.
		// (2) closeAll consumes this teardownOnce itself via CloseWithStatus
		//     (see CloseWithStatus below). Removing the go and calling it synchronously
		//     would re-enter teardownOnce.Do while teardownOnce.Do is running,
		//     and sync.Once would deadlock itself.
		m.teardownOnce.Do(func() {
			go m.closeAll(fmt.Sprintf("No sub-connection has been connected for %v.", m.noConnected.timeout))
		})
		return
	}

	m.setOverallStatus(newStatus)

	if newStatus == MultiOverallStatusDisconnected && totalCount > 0 { // Already canceled when there are no transports.
		m.logger.Infof(m.ctx, "Overall status is Disconnected. Shutting down multi-transport.")
		m.cancel() // Shut down when the state becomes Disconnected.
		// cancel does not propagate to the sub-connections, so Close is needed
		// to reclaim goroutines (see the comment on closeAll). Share the same once
		// as the threshold-exceeded path so that Close is not called twice from both.
		m.teardownOnce.Do(func() { go m.closeAll("All sub-connections are disconnected.") })
	}
}

// closeAll closes multi.Transport itself and all sub-connections.
//
// m.cancel() alone is not enough. The ctx of reconnect.Transport derives from context.Background()
// and has no parent-child relationship with m.ctx (transport/reconnect/transport.go:224),
// so cancel does not propagate to the sub-connections. Without Close, when the v4 protocol is enabled,
// heartbeatLoop keeps running (on the threshold-exceeded path, dial retries and readLoop also
// keep running. On the all-subs-Disconnected path, each sub is already Disconnected,
// so dial / readLoop do not remain).
//
// Always call it from a goroutine separate from the status callback (see the comment on updateOverallStatus).
//
// This goroutine (go m.closeAll(...)) is not joined by anyone. In CloseWithStatus,
// the sub's CloseWithStatus may wait for an in-flight dial in waitForReconnectToFinish(),
// so this goroutine may stay alive even after mt.Close() returns.
// The caller cannot rely on waiting for closeAll to finish
// (this is intentional, because joining would make it wait on itself).
func (m *Transport) closeAll(reason string) {
	m.logger.Warnf(m.ctx, "%s Shutting down multi-transport.", reason)
	if err := m.CloseWithStatus(transport.CloseStatusNormal); err != nil {
		m.logger.Warnf(m.ctx, "Failed to close multi-transport: %v", err)
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
	// Return the first available Transport.
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
//
// 注意: セレクタの Get から呼ばれる場合、writeOnce が保持する m.mu.RLock()
// の下で再帰的に RLock を取ることになる（m.mu のフィールドコメント参照）。
func (m *Transport) Transports() TransportMap {
	m.mu.RLock()
	defer m.mu.RUnlock()
	// Return a copy to make it thread-safe.
	result := make(TransportMap, len(m.transportMap))
	for k, v := range m.transportMap {
		result[k] = v
	}
	return result
}

// Close implements Transport.
func (m *Transport) CloseWithStatus(status transport.CloseStatus) error {
	m.cancel()
	// If a sub becomes Disconnected as a result of an explicit Close, the teardown path (closeAll)
	// fires via updateOverallStatus and Close would be re-entered. Consume the once here to prevent that.
	// If teardown came first, the once was already consumed when closeAll was started, so this is a no-op.
	m.teardownOnce.Do(func() {})

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
	// Return the NegotiationParams of the first available Transport.
	for _, tr := range m.transportMap {
		return tr.NegotiationParams()
	}
	// If there are no transports, return empty NegotiationParams.
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

// writeRetryInterval is the retry interval when all sub-connections are temporarily unable to write.
const writeRetryInterval = 10 * time.Millisecond

// errAllNotConnected is an internal marker meaning that all sub-connections failed
// with ErrNotConnected before calling the underlying Write.
// A write that failed with this error can be retried safely because no partial send occurred.
var errAllNotConnected = errors.New("all sub-connections are not connected")

// Write implements Transport.
//
// 全 sub-connection が「下層 Write を呼ぶ前に」ErrNotConnected で失敗した場合は、
// いずれかの回線が復帰するか、multi.Transport 全体が畳まれる（NoConnectedTransportTimeout
// 超過 / Close）まで再試行を続けます。
//
// これは reconnect.Transport の waitForWritable が StatusConnecting では待機する一方、
// StatusReconnecting かつ無期限リトライでは即エラーを返す（transport/reconnect/transport.go:419-427）
// 非対称性を、multi.Transport のレイヤで吸収するためです。multi 構成では
// 個別 sub-connection に無期限リトライをさせ、全体の生死判定を親に集約するので、
// 「親がまだ諦めていない間は書き込みも諦めない」という扱いに揃えます。
//
// 挙動変更（writeRaw の TOCTOU 対策、transport/reconnect/transport.go）: 下層
// tr.Write が errors.ErrConnectionClosed と判定できるエラーで失敗した場合、
// reconnect.Transport はそれを ErrNotConnected に変換して返すようになりました。
// そのため、全 sub がこの種のエラーで失敗するケースは、以前の「joined error を
// 即座に返す」から、上記の内部リトライで待ち続ける挙動に変わっています
// （部分送信が起きていないため再試行しても安全、という前提自体は変わりません）。
//
// 再試行するのは ErrNotConnected のときだけです。context.DeadlineExceeded /
// context.Canceled は下層 Write の途中で発火した可能性があり、再送すると
// 重複送信になるため対象外です（フォールバック判定の isFallbackableWriteError とは
// 対象が異なることに注意）。
func (m *Transport) Write(bs []byte) error {
	for {
		err := m.writeOnce(bs)
		if err == nil {
			return nil
		}
		if !errors.Is(err, errAllNotConnected) {
			return err
		}
		select {
		case <-m.ctx.Done():
			return err
		case <-time.After(writeRetryInterval):
		}
	}
}

// writeOnce attempts a single write.
//
// If the write to the sub-conn chosen by the selector fails with a fallbackable error
// (see isFallbackableWriteError), the remaining sub-conns are tried in turn.
// This covers the following:
//   - A race between the selector's status-aware decision (SelectAvailableTransport) and the write
//     (the target sub-conn transitions to Reconnecting right after being selected).
//   - A case where the underlying Write stalls until writeTimeout due to a silent drop on NIC outbound
//     and then exits with context.DeadlineExceeded (a partial send is possible, but this is left to
//     de-dup based on the sequence_number in the iSCP application layer, prioritizing avoiding session tear-down).
//
// Other errors (protocol violations, encoding errors, etc.) are returned as is without fallback.
//
// Note: the current implementation is synchronous, and queueSize (the send queue size) is always treated as 0.
// If asynchronous sending (WriteAsync) is supported in the future, the following is needed:
//   - Add a queueSize field (uint64, for atomic operations) to the Transport struct
//   - Before sending: atomic.AddUint64(&m.queueSize, uint64(len(bs)))
//   - After sending: atomic.AddUint64(&m.queueSize, ^uint64(len(bs)-1)) // subtract
//   - Call ECFUpdater.SetQueueSize()
//
// This lets the inequalities (x_f, x_s) of the ECF algorithm take the amount of pending data into account.
func (m *Transport) writeOnce(bs []byte) error {
	// This RLock becomes a recursive RLock on the same goroutine via
	// transportSelector.Get -> mt.Transports() (see the field comment on m.mu).
	// It is safe only as long as no writer is added to m.mu.
	m.mu.RLock()
	defer m.mu.RUnlock()

	selectedID := m.transportSelector.Get(m.ctx, int64(len(bs)))
	if selectedID == "" {
		return transport.ErrAlreadyClosed
	}

	firstErr := m.transportMap[selectedID].Write(bs)
	if firstErr == nil {
		return nil
	}
	// Fallback decision:
	//   - ErrNotConnected: failed before calling the underlying Write (no partial send, fallback is safe).
	//   - ErrConnectionClosed: the underlying transport was closed (the write side is already released, fallback is safe).
	//   - context.DeadlineExceeded / context.Canceled: deadline / cancel fired in the underlying write
	//     (the path where a silent drop on NIC outbound stalls TCP and hits writeTimeout).
	//     A partial send is possible, but on the premise that the iSCP application layer (sequence_number)
	//     detects and de-dups duplicates, falling back is preferred over staying on a broken sub-conn.
	//   - Anything else is a protocol violation / encoding error, etc., so it is returned as is.
	if !isFallbackableWriteError(firstErr) {
		return firstErr
	}

	// Fallback: try the remaining sub-conns in turn.
	errs := []error{firstErr}
	allNotConnected := errors.Is(firstErr, reconnect.ErrNotConnected)
	for id, tr := range m.transportMap {
		if id == selectedID {
			continue
		}
		err := tr.Write(bs)
		if err == nil {
			// A successful fallback returns no error to the caller, so unless it is recorded here,
			// the fact that "the selected sub-conn stalled until writeTimeout" is visible neither to the application
			// nor to operations.
			//
			// Only deadline-caused fallbacks are recorded.
			// Fallbacks due to ErrNotConnected / ErrAlreadyClosed can occur on every write during reconnection,
			// so warning on all of them would produce logs at the same frequency as the delivery rate
			// (those can be traced in the reconnect-side logs). A deadline means WriteTimeout was reached,
			// and its frequency is capped at once per writeTimeout.
			if errors.Is(firstErr, context.DeadlineExceeded) {
				m.logger.Warnf(m.ctx, "Write fell back from transport %s to %s after the write timed out: %v", selectedID, id, firstErr)
			}
			return nil
		}
		errs = append(errs, err)
		if !errors.Is(err, reconnect.ErrNotConnected) {
			allNotConnected = false
		}
		// If a non-fallbackable error is hit midway, stop there (do not resend to the rest).
		if !isFallbackableWriteError(err) {
			break
		}
	}
	joined := fmt.Errorf("multi write: all sub-connections failed: %w", errors.Join(errs...))
	if allNotConnected {
		// All failed before calling the underlying Write, so resending does not duplicate.
		return fmt.Errorf("%w: %w", errAllNotConnected, joined)
	}
	return joined
}

// isFallbackableWriteError determines whether an error from a failed multi.Write may be
// retried on the remaining sub-conns by fallback. See the comment on Write for details.
func isFallbackableWriteError(err error) bool {
	switch {
	case errors.Is(err, reconnect.ErrNotConnected),
		errors.Is(err, transport.ErrAlreadyClosed),
		errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, context.Canceled):
		return true
	}
	return false
}

func (m *Transport) readLoopTransport(tID transport.SubConnectionID, t *reconnect.Transport) {
	// readLoopWg.Add(1) is called in readLoop() before starting the goroutine.
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

// metricsUpdateInterval is the interval of metrics updates.
const metricsUpdateInterval = 100 * time.Millisecond

// metricsUpdateLoop periodically performs metrics updates for metrics-based selectors.
func (m *Transport) metricsUpdateLoop() {
	m.logger.Infof(m.ctx, "Starting metrics update loop with interval %v", metricsUpdateInterval)
	defer m.logger.Infof(m.ctx, "Stopping metrics update loop")

	ticker := time.NewTicker(metricsUpdateInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.updateMetrics()
		}
	}
}

// updateMetrics gets the metrics of each transport and updates the metrics-based selector.
func (m *Transport) updateMetrics() {
	if m.metricsUpdater == nil {
		return
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	for tID, tr := range m.transportMap {
		// Get the MetricsProvider from reconnect.Transport.
		// Confirm that reconnect.Transport implements the MetricsProvider interface.
		info := NewTransportInfo(tID, tr)
		m.metricsUpdater.UpdateTransport(tID, info)
	}
}
