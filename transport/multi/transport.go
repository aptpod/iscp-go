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
	// 注意: m.mu には Lock()（writer）を追加してはならない。
	// writeOnce は m.mu.RLock() を保持したまま transportSelector.Get を呼び、
	// 全セレクタ実装に Get の中で mt.Transports()（m.mu.RLock() を再取得）を
	// 呼ぶ経路があるため、同一 goroutine での再帰 RLock が起こりうる。sync.RWMutex の
	// 契約上、再帰 RLock は「間に writer が割り込むと 2 回目の RLock が
	// ブロックして自己デッドロックする」ため禁止されている。現状は
	// production コードに m.mu の writer が存在しない（transportMap は
	// 構築後に変更されない）ことだけで成立しており、writer を 1 箇所でも
	// 足すとこの経路が deadlock になりうる。構造の解消は別課題として起票済み。
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
	// noConnected は「接続済みの sub-connection が 1 本も無い状態」の継続時間を追跡する。
	noConnected *noConnectedTracker
	// teardownOnce は teardown を 1 回だけ実行させる（閾値超過経路・全 sub Disconnected 経路で共有）。
	// CloseWithStatus の先頭でも消費され、明示 Close 経由で teardown 経路が
	// 二重に発火（Close の再入）しないよう抑止する役割を兼ねる。
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
	// minStatusCheckInterval は、NoConnectedTransportTimeout に合わせて監視間隔を
	// 詰めるときの下限です。極端に短い閾値で ticker が高頻度に回るのを防ぎます。
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
	// 閾値の判定は statusCheckInterval ごとの level-trigger でしか進まないため、
	// 閾値が監視間隔より短いと判定が最大で監視間隔ぶん遅れる。
	// 呼び出し側が短い閾値を指定したときは監視間隔を自動的に詰める。
	if c.NoConnectedTransportTimeout > 0 && c.NoConnectedTransportTimeout < m.statusCheckInterval {
		m.statusCheckInterval = max(c.NoConnectedTransportTimeout/2, minStatusCheckInterval)
	}

	m.ctx, m.cancel = context.WithCancel(context.Background())

	// ECFSelector または MultiTransportSetter をサポートするセレクタの初期化
	if setter, ok := c.TransportSelector.(MultiTransportSetter); ok {
		setter.SetMultiTransport(m)
	}

	// TransportMetricsUpdater をサポートするセレクタの場合、メトリクス更新を有効化
	if updater, ok := c.TransportSelector.(TransportMetricsUpdater); ok {
		m.metricsUpdater = updater
		m.metricsUpdaterEnabled = true
		// ロガーを設定
		updater.SetLogger(c.Logger)
		// 初回のメトリクス更新
		m.updateMetrics()
	}

	// 各 reconnect.Transport にステータス変更コールバックを設定し、
	// イベント駆動で全体ステータスを更新する
	for _, t := range m.transportMap {
		t.SetOnStatusChange(func(oldStatus, newStatus reconnect.Status) {
			m.updateOverallStatus()
		})
	}

	go m.readLoop()
	go m.statusMonitorLoop()

	// メトリクス更新が有効な場合、更新ループを開始
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

	// マップのスナップショットを取得してイテレーションする
	// これにより、readLoopTransport の defer による delete との競合を回避
	m.mu.RLock()
	transports := make(map[transport.SubConnectionID]*reconnect.Transport, len(m.transportMap))
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
		connectingCount   int
		reconnectingCount int
		disconnectedCount int
		totalCount        = len(m.transportMap)
	)

	for tID, tr := range m.transportMap {
		status := tr.Status() // StatusProviderの実装が前提

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
	} else if connectingCount == totalCount {
		newStatus = MultiOverallStatusAllConnecting
	} else if reconnectingCount > 0 || connectingCount > 0 {
		newStatus = MultiOverallStatusAllReconnecting
	} else { // connectedCount == 0 && reconnectingCount == 0 && connectingCount == 0
		// この時点で残りは全て StatusDisconnected のはず
		newStatus = MultiOverallStatusDisconnected
	}

	// 接続済みが 1 本も無い状態が閾値を超えて続いたら、全体を諦める。
	// AllConnecting（一度も接続していない）と AllReconnecting（接続後に全部切れた）は
	// 区別しない。起動直後も閾値ぶんの猶予があるため、正常な過渡状態は誤検知しない。
	if m.noConnected.observe(connectedCount > 0) {
		newStatus = MultiOverallStatusDisconnected
		m.setOverallStatus(newStatus)
		// teardown をこの場で同期実行してはいけない。理由は 2 つある。
		// (1) updateOverallStatus は reconnect の status callback から同期
		//     呼び出しされることがあり、callback の呼び出し元は reconnectMu
		//     や r.mu を保持している（transport/reconnect/transport.go:640-652,
		//     :767）。callback の延長で sub.CloseWithStatus を呼ぶと自己
		//     deadlock する。
		// (2) closeAll は CloseWithStatus 経由でこの teardownOnce 自体を
		//     消費する（下記 CloseWithStatus 参照）。go を外して同期呼び出し
		//     にすると、teardownOnce.Do の実行中に同じ teardownOnce.Do を
		//     再入することになり、sync.Once が自己 deadlock する。
		m.teardownOnce.Do(func() {
			go m.closeAll(fmt.Sprintf("No sub-connection has been connected for %v.", m.noConnected.timeout))
		})
		return
	}

	m.setOverallStatus(newStatus)

	if newStatus == MultiOverallStatusDisconnected && totalCount > 0 { // トランスポートが0の場合は既にcancel済み
		m.logger.Infof(m.ctx, "Overall status is Disconnected. Shutting down multi-transport.")
		m.cancel() // Disconnected状態になったら終了
		// cancel は sub-connection へ伝播しないため、goroutine を回収するには
		// Close が要る（closeAll のコメント参照）。閾値超過経路と同じ once を
		// 共有し、両方から二重に Close しないようにする。
		m.teardownOnce.Do(func() { go m.closeAll("All sub-connections are disconnected.") })
	}
}

// closeAll は multi.Transport 自身と全 sub-connection を Close します。
//
// m.cancel() だけでは不十分です。reconnect.Transport の ctx は context.Background()
// 由来で m.ctx と親子関係がないため（transport/reconnect/transport.go:224）、
// cancel は sub-connection へ伝播しません。Close しなければ、v4 プロトコル有効時は
// heartbeatLoop が残り続けます（閾値超過経路では、さらに dial の再試行と readLoop も
// 残り続けます。全 sub Disconnected 経路は各 sub が既に Disconnected 済みのため
// dial / readLoop は残りません）。
//
// 必ず status callback とは別の goroutine から呼んでください（updateOverallStatus のコメント参照）。
//
// この goroutine（go m.closeAll(...)）は誰にも join されず、CloseWithStatus 内で
// sub の CloseWithStatus が waitForReconnectToFinish() で in-flight な dial を
// 待つことがあるため、mt.Close() の戻りより後までこの goroutine が生きていることが
// あります。呼び出し側は closeAll の完了を待って良い保証はありません
// （join すると自己待ちになるため、意図的にそうしています）。
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
//
// 注意: セレクタの Get から呼ばれる場合、writeOnce が保持する m.mu.RLock()
// の下で再帰的に RLock を取ることになる（m.mu のフィールドコメント参照）。
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
	// 明示 Close の延長で sub が Disconnected になると、updateOverallStatus 経由で
	// teardown 経路（closeAll）が発火し Close が再入してしまう。once をここで消費して抑止する。
	// teardown が先行しているケースでは、closeAll を起動した時点で既に消費済みなので no-op。
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

// writeRetryInterval は、全 sub-connection が一時的に書き込めないときの再試行間隔です。
const writeRetryInterval = 10 * time.Millisecond

// errAllNotConnected は、全 sub-connection が下層 Write を呼ぶ前に
// ErrNotConnected で失敗したことを表す内部マーカーです。
// このエラーで失敗した書き込みは、部分送信が起きていないため安全に再試行できます。
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

// writeOnce は 1 回ぶんの書き込みを試みます。
//
// セレクタが選んだ sub-conn への書き込みがフォールバック対象エラー
// (isFallbackableWriteError 参照) で失敗した場合、残りの sub-conn を順に試す。
// これにより以下をカバーする:
//   - selector の status-aware 判定 (SelectAvailableTransport) と書き込み実行の間の race
//     (選択直後に対象 sub-conn が Reconnecting へ遷移する)
//   - NIC outbound の silent drop 等で下層 Write が writeTimeout まで stall した後に
//     context.DeadlineExceeded で抜けるケース (部分送信の可能性はあるが iSCP アプリ層の
//     sequence_number ベース de-dup に委ね、セッション tear-down を避けることを優先)
//
// 上記以外のエラー (プロトコル違反・エンコードエラー等) はフォールバックせずそのまま返す。
//
// 注意: 現在の実装は同期的であり、queueSize（送信待ちキューサイズ）は常に0として扱われます。
// 将来的に非同期送信（WriteAsync）をサポートする場合は、以下の対応が必要です:
//   - Transport構造体にqueueSizeフィールド（uint64, atomic操作用）を追加
//   - 送信前: atomic.AddUint64(&m.queueSize, uint64(len(bs)))
//   - 送信後: atomic.AddUint64(&m.queueSize, ^uint64(len(bs)-1)) // 減算
//   - ECFUpdater.SetQueueSize() の呼び出し
//
// これにより、ECFアルゴリズムの不等式（x_f, x_s）で送信待ちデータ量を考慮できます。
func (m *Transport) writeOnce(bs []byte) error {
	// この RLock は transportSelector.Get → mt.Transports() 経由で同一
	// goroutine 上の再帰 RLock になる（m.mu のフィールドコメント参照）。
	// m.mu に writer を追加しない限りにおいてのみ安全。
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
	// フォールバック判定:
	//   - ErrNotConnected: 下層 Write を呼ぶ前に失敗 (部分送信なし、fallback 安全)
	//   - ErrConnectionClosed: 下層 transport が閉じられた (write 側は既に解放済み、fallback 安全)
	//   - context.DeadlineExceeded / context.Canceled: 下層 write で deadline / cancel
	//     発火 (NIC outbound が silent drop 等で TCP stall → writeTimeout hit するパス)。
	//     部分送信の可能性はゼロではないが、iSCP アプリ層 (sequence_number) で重複検知・de-dup
	//     される前提で、壊れている sub-conn に留まるよりフォールバックを優先する。
	//   - 上記以外はプロトコル違反 / エンコードエラー等なのでそのまま返す。
	if !isFallbackableWriteError(firstErr) {
		return firstErr
	}

	// フォールバック: 残りの sub-conn を順に試す。
	errs := []error{firstErr}
	allNotConnected := errors.Is(firstErr, reconnect.ErrNotConnected)
	for id, tr := range m.transportMap {
		if id == selectedID {
			continue
		}
		err := tr.Write(bs)
		if err == nil {
			// フォールバックが成功すると上位にはエラーを返さないため、ここで記録しないと
			// 「選択された sub-conn が writeTimeout まで stall した」ことがアプリからも
			// 運用からも見えない。
			//
			// ただし記録するのは deadline 由来のフォールバックだけに絞る。
			// ErrNotConnected / ErrAlreadyClosed でのフォールバックは再接続中に毎 write
			// 発生しうるため、一律に警告を出すと配信レートと同じ頻度でログが出る
			// (そちらは reconnect 側のログで追える)。deadline は WriteTimeout に達した
			// ことを意味し、頻度も writeTimeout ごとに 1 回で頭打ちになる。
			if errors.Is(firstErr, context.DeadlineExceeded) {
				m.logger.Warnf(m.ctx, "Write fell back from transport %s to %s after the write timed out: %v", selectedID, id, firstErr)
			}
			return nil
		}
		errs = append(errs, err)
		if !errors.Is(err, reconnect.ErrNotConnected) {
			allNotConnected = false
		}
		// 途中で fallback 不可エラーを掴んだらそこで停止（後続に再送しない）。
		if !isFallbackableWriteError(err) {
			break
		}
	}
	joined := fmt.Errorf("multi write: all sub-connections failed: %w", errors.Join(errs...))
	if allNotConnected {
		// 下層 Write を呼ぶ前に全滅したので、再送しても重複しない。
		return fmt.Errorf("%w: %w", errAllNotConnected, joined)
	}
	return joined
}

// isFallbackableWriteError は multi.Write 失敗時に残りの sub-conn へ fallback して
// 良いエラーかを判定する。詳細は Write のコメント参照。
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
	// readLoopWg.Add(1) は readLoop() で goroutine 開始前に呼ばれる
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

// metricsUpdateInterval はメトリクス更新の間隔です。
const metricsUpdateInterval = 100 * time.Millisecond

// metricsUpdateLoop はメトリクスベースのセレクタ用のメトリクス更新を定期的に実行します。
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

// updateMetrics は各トランスポートのメトリクスを取得し、メトリクスベースのセレクタに更新します。
func (m *Transport) updateMetrics() {
	if m.metricsUpdater == nil {
		return
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	for tID, tr := range m.transportMap {
		// reconnect.Transport から MetricsProvider を取得
		// reconnect.Transport が MetricsProvider インターフェースを実装していることを確認
		info := NewTransportInfo(tID, tr)
		m.metricsUpdater.UpdateTransport(tID, info)
	}
}
