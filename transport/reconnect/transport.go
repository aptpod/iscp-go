package reconnect

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/aptpod/iscp-go/errors"
	"github.com/aptpod/iscp-go/log"
	"github.com/aptpod/iscp-go/transport"
	"github.com/aptpod/iscp-go/transport/metrics"
)

var (
	_ transport.Transport = (*Transport)(nil)
	_ transport.Closer    = (*Transport)(nil)
)

type readRes struct {
	bs  []byte
	err error
}

type writeReq struct {
	id int64
	bs []byte
}

type writeRes struct {
	err error
}

// Connector は、トランスポート接続を確立するためのインターフェースです。
type Connector interface {
	Connect() (transport.Transport, error)
}

// TransportConnectorFunc は、Connector インターフェースを実装する関数型です。
type TransportConnectorFunc func() (transport.Transport, error)

func (f TransportConnectorFunc) Connect() (transport.Transport, error) {
	return f()
}

// Transport は、自動再接続機能を持つトランスポート層です。
type Transport struct {
	reconnector          Connector
	transport            transport.Transport
	mu                   sync.RWMutex
	maxReconnectAttempts int
	reconnectInterval    time.Duration
	pingInterval         time.Duration
	readTimeout          time.Duration

	readResCh chan *readRes

	writeID    atomic.Int64
	writeReqCh chan writeReq
	writeResMu sync.RWMutex
	writeResCh map[int64]chan writeRes

	// Ping/Pong シーケンス追跡
	pingSeq      atomic.Uint32        // 送信ping のシーケンス番号
	pongSeqMu    sync.RWMutex         // pendingPongs マップを保護
	pendingPongs map[uint32]time.Time // シーケンス番号 -> 送信時刻（RTT測定用）

	// トランスポートメトリクスプロバイダー（RTT、CWND など）
	// プロバイダーは Stop() を介して独自のライフサイクルを管理します。
	metricsProvider metrics.MetricsProvider

	// アプリケーションレベルRTT（Ping/Pongから計算）
	appRTTMu           sync.RWMutex
	appRTT             time.Duration // 最新のRTT値（スムージング適用済み）
	rttSmoothingFactor float64       // 0.0-1.0（1.0=スムージングなし）
	rttSource          RTTSource     // RTTの取得元

	ctx    context.Context
	cancel context.CancelFunc
	logger log.Logger

	statusMu sync.RWMutex
	status   Status

	initialConnectDoneCh chan error
	initialConnectOnce   sync.Once
	negotiationParams    transport.NegotiationParams
}

// Dialer は、再接続機能を持つトランスポートダイアラーです。
type Dialer struct {
	DialConfig *DialConfig
}

// NewDialer は、新しい Dialer を作成します。
func NewDialer(c *DialConfig) *Dialer {
	return &Dialer{DialConfig: c}
}

// Dial は、指定された設定でトランスポートを確立します。
func (d *Dialer) Dial(dc transport.DialConfig) (transport.Transport, error) {
	c := *d.DialConfig
	c.DialConfig.TransportID = dc.TransportID
	return Dial(c)
}

// DialConfig は、再接続トランスポートの設定を保持します。

// RTTSource はRTTの取得元を指定する型です。
type RTTSource int

const (
	// RTTSourceAuto はアプリケーションRTTを優先し、利用不可の場合はメトリクスプロバイダーを使用します（デフォルト）。
	RTTSourceAuto RTTSource = iota
	// RTTSourceApp はアプリケーションRTT（Ping/Pong）のみを使用します。
	RTTSourceApp
	// RTTSourceMetrics はメトリクスプロバイダーのRTTのみを使用します。
	RTTSourceMetrics
)

type DialConfig struct {
	Dialer               transport.Dialer
	DialConfig           transport.DialConfig
	MaxReconnectAttempts int
	ReconnectInterval    time.Duration
	PingInterval         time.Duration
	ReadTimeout          time.Duration
	Logger               log.Logger

	// RTTSmoothingFactor はアプリケーションRTTのスムージング係数（0.0-1.0）
	// 1.0 = スムージングなし（テスト用、即座に反映）
	// 0.125 = RFC6298互換
	// 0（未指定）の場合はデフォルトで1.0（スムージングなし）を使用
	RTTSmoothingFactor float64

	// RTTSource はRTTの取得元を指定します。
	// デフォルト（RTTSourceAuto）: アプリケーションRTT優先、利用不可時はメトリクスプロバイダー
	RTTSource RTTSource
}

// Dial は、指定された設定で再接続トランスポートを作成します。
func Dial(c DialConfig) (*Transport, error) {
	if c.Dialer == nil {
		return nil, fmt.Errorf("dialer is required")
	}
	if c.ReconnectInterval == 0 {
		c.ReconnectInterval = time.Second
	}
	if c.MaxReconnectAttempts == 0 {
		c.MaxReconnectAttempts = 30
	}
	// MaxReconnectAttempts < 0 は無制限を意味します

	// ネゴシエーションパラメータを取得し、利用可能な場合はping/readタイムアウトを設定
	negParams := c.DialConfig.NegotiationParams()

	// ping間隔を設定: ネゴシエーションパラメータ、次に設定、最後にデフォルトの優先順位
	pingInterval := c.PingInterval
	if negParams.PingInterval != nil && *negParams.PingInterval > 0 {
		pingInterval = time.Duration(*negParams.PingInterval) * time.Millisecond
	} else if pingInterval == 0 {
		pingInterval = 10 * time.Second
	}

	// readタイムアウトを設定: ネゴシエーションパラメータ、次に設定、最後にデフォルトの優先順位
	readTimeout := c.ReadTimeout
	if negParams.ReadTimeout != nil && *negParams.ReadTimeout > 0 {
		readTimeout = time.Duration(*negParams.ReadTimeout) * time.Millisecond
	} else if readTimeout == 0 {
		readTimeout = 30 * time.Second
	}

	if c.Logger == nil {
		c.Logger = log.NewNop()
	}
	if c.DialConfig.TransportID == "" {
		c.DialConfig.TransportID = transport.TransportID(uuid.New().String())
	}

	// まだ設定されていない場合、ping間隔とreadタイムアウトをネゴシエーションパラメータに設定
	if negParams.PingInterval == nil {
		intervalMs := int(pingInterval.Milliseconds())
		negParams.PingInterval = &intervalMs
	}
	if negParams.ReadTimeout == nil {
		timeoutMs := int(readTimeout.Milliseconds())
		negParams.ReadTimeout = &timeoutMs
	}

	// RTTスムージング係数のデフォルト設定（0の場合は1.0=スムージングなし）
	rttSmoothingFactor := c.RTTSmoothingFactor
	if rttSmoothingFactor == 0 {
		rttSmoothingFactor = 1.0 // デフォルト: スムージングなし
	}

	// まずTransportインスタンスを作成し、実際の接続はバックグラウンドで実行

	t := &Transport{
		reconnector: TransportConnectorFunc(func() (transport.Transport, error) {
			return c.Dialer.Dial(c.DialConfig)
		}),
		transport:            nil, // 初期状態では内部トランスポートは nil
		mu:                   sync.RWMutex{},
		maxReconnectAttempts: c.MaxReconnectAttempts,
		reconnectInterval:    c.ReconnectInterval,
		pingInterval:         pingInterval,
		readTimeout:          readTimeout,
		readResCh:            make(chan *readRes, 1024),
		writeReqCh:           make(chan writeReq, 1024),
		writeResCh:           make(map[int64]chan writeRes),
		pendingPongs:         make(map[uint32]time.Time),
		metricsProvider:      metrics.NewNopMetricsProvider(), // noop で初期化
		rttSmoothingFactor:   rttSmoothingFactor,
		rttSource:            c.RTTSource,
		ctx:                  nil,
		cancel:               nil,
		logger:               c.Logger,
		statusMu:             sync.RWMutex{},
		status:               StatusConnecting, // 新しい "connecting" ステータス
		initialConnectDoneCh: make(chan error, 1),
		negotiationParams:    negParams,
	}
	t.ctx, t.cancel = context.WithCancel(context.Background())

	// バックグラウンドで接続プロセスを実行
	go t.initialConnect(c.Dialer, c.DialConfig)

	go t.pingLoop()
	go t.readLoop()
	go t.writeLoop()
	return t, nil
}

// initialConnect は、バックグラウンドで初期接続試行を実行します。
func (r *Transport) initialConnect(dialer transport.Dialer, dialConfig transport.DialConfig) {
	r.logger.Infof(r.ctx, "Starting initial connection attempts...")
	var err error

	doneProcess := func(err error, status Status) {
		r.statusMu.Lock()
		r.status = status
		r.statusMu.Unlock()
		r.initialConnectOnce.Do(func() {
			r.initialConnectDoneCh <- err
			close(r.initialConnectDoneCh)
		})
		if err != nil {
			r.cancel() // Close all if initial connect fails
		}
	}

	for i := 0; ; i++ {
		if r.maxReconnectAttempts >= 0 && i >= r.maxReconnectAttempts {
			// すべての試行が失敗
			doneProcess(err, StatusDisconnected)
			return
		}
		if r.closed() {
			r.logger.Infof(r.ctx, "Initial connection canceled.")
			doneProcess(errors.ErrConnectionClosed, StatusDisconnected)
			return
		}
		if r.maxReconnectAttempts < 0 {
			r.logger.Infof(r.ctx, "Attempting to connect (%d/unlimited)...", i+1)
		} else {
			r.logger.Infof(r.ctx, "Attempting to connect (%d/%d)...", i+1, r.maxReconnectAttempts)
		}
		currentTr, currentErr := dialer.Dial(dialConfig)
		err = currentErr
		if currentErr == nil {
			if _, ok := currentTr.(transport.Closer); !ok {
				err = fmt.Errorf("transport does not implement Closer")
				r.logger.Errorf(r.ctx, "Initial connection failed as transport does not implement Closer: %v", err)
				doneProcess(err, StatusDisconnected)
				return
			}
			r.mu.Lock()
			r.transport = currentTr
			// 新しいトランスポートからメトリクスプロバイダーを初期化
			if ms, ok := currentTr.(transport.MetricsSupporter); ok {
				r.metricsProvider = ms.MetricsProvider()
			} else {
				r.metricsProvider = metrics.NewNopMetricsProvider()
			}
			r.mu.Unlock()
			doneProcess(nil, StatusConnected) // 接続成功時にステータスを更新
			r.logger.Infof(r.ctx, "Successfully connected.")
			return
		}
		r.logger.Warnf(r.ctx, "Initial connection attempt failed: %v", currentErr)
		time.Sleep(r.reconnectInterval)
	}
}

// waitForConnection は、初期接続が完了するかコンテキストがキャンセルされるまで待機します。
// 接続が成功した場合は nil を返し、失敗した場合はエラーを返します。
func (r *Transport) waitForConnection(ctx context.Context) error {
	currentStatus := r.Status()
	if currentStatus == StatusConnected {
		return nil
	}
	if currentStatus == StatusDisconnected && !r.closed() { // closed() は ctx.Done() をチェックするため、ここではステータスのみで判断
		return errors.New("transport is disconnected")
	}

	if currentStatus == StatusConnecting {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.ctx.Done(): // Transport自身のコンテキストも監視
			return errors.ErrConnectionClosed
		case err, ok := <-r.initialConnectDoneCh:
			if !ok {
				// チャネルがすでにクローズされている（initialConnect が完了済み）
				// この時点でのステータスを信頼
				if r.Status() == StatusConnected {
					return nil
				}
				return errors.New("initial connection previously failed or channel closed unexpectedly")
			}
			// initialConnectDoneCh から値を受信（initialConnect が今完了した）
			if err != nil {
				// initialConnect がエラーで完了
				return fmt.Errorf("initial connection attempt failed: %w", err)
			}
			// initialConnect が正常に完了（err == nil）
			if r.Status() != StatusConnected {
				// 通知は成功したがステータスが一致しない（競合状態は unlikely だが念のため）
				return errors.New("connection status inconsistent after initial connect success notification")
			}
			return nil
		}
	}
	// StatusReconnecting の場合、ここでは待機せず、各操作での再接続プロセスに任せる
	// StatusDisconnected の場合、上記で既に処理済みか、initialConnect 完了と失敗の結果
	return nil
}

func (r *Transport) nextID() int64 {
	return r.writeID.Add(1)
}

func (r *Transport) pingLoop() {
	r.logger.Infof(r.ctx, "Starting ping loop")
	// 内部トランスポートが確立されるまで待機
	if err := r.waitForConnection(r.ctx); err != nil {
		r.logger.Errorf(r.ctx, "Ping loop canceled, failed to establish connection: %v", err)
		return
	}

	ticker := time.NewTicker(r.pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.ctx.Done():
			r.logger.Infof(r.ctx, "Ping loop stopped")
			return
		case <-ticker.C:
			// シーケンス番号をインクリメントして ping を送信
			seq := r.pingSeq.Add(1)

			// RTT 測定のために送信時刻を記録
			r.pongSeqMu.Lock()
			r.pendingPongs[seq] = time.Now()
			r.pongSeqMu.Unlock()

			// シーケンス番号付きの ping を送信
			pingMsg, _ := (&PingMessage{Sequence: seq}).MarshalBinary()
			if err := r.writeReqRes(pingMsg); err != nil {
				r.logger.Errorf(r.ctx, "Failed to send ping (seq=%d): %v", seq, err)
				return
			}
			r.logger.Debugf(r.ctx, "Sent ping (seq=%d)", seq)
		}
	}
}

func (r *Transport) writeReqRes(bs []byte) error {
	id := r.nextID()
	resCh := make(chan writeRes, 1)

	r.writeResMu.Lock()
	r.writeResCh[id] = resCh
	r.writeResMu.Unlock()

	writeOrDone(r.ctx, writeReq{id: id, bs: bs}, r.writeReqCh)

	r.writeResMu.RLock()
	ch := r.writeResCh[id]
	r.writeResMu.RUnlock()

	res, ok := readOrDoneOne(r.ctx, ch)

	r.writeResMu.Lock()
	delete(r.writeResCh, id) // Cleanup the channel after use
	r.writeResMu.Unlock()

	if !ok {
		return errors.ErrConnectionClosed
	}
	return res.err
}

func (r *Transport) writeLoop() {
	// writeCh をクローズする必要はありません
	r.logger.Infof(r.ctx, "Starting write loop")
	for {
		select {
		case <-r.ctx.Done():
			return
		case data := <-r.writeReqCh:
			r.mu.RLock()
			trEstablished := r.transport != nil
			r.mu.RUnlock()

			if !trEstablished {
				if r.closed() {
					return
				}
				// 内部トランスポートがまだ確立されていない場合、接続を待機
				if err := r.waitForConnection(r.ctx); err != nil {
					writeOrDone(r.ctx, writeRes{err: fmt.Errorf("failed to establish initial connection: %w", err)}, r.writeResCh[data.id])
					continue
				}
				// waitForConnection 後に trEstablished を再度チェック
				r.mu.RLock()
				trEstablished = r.transport != nil
				r.mu.RUnlock()
				if !trEstablished { // それでもまだ確立されていない場合はエラー
					writeOrDone(r.ctx, writeRes{err: errors.New("transport not connected after wait")}, r.writeResCh[data.id])
					continue
				}
			}

			for {
				r.mu.RLock()
				tr := r.transport
				r.mu.RUnlock()
				err := tr.Write(data.bs)
				if err != nil {
					if r.closed() {
						return
					}
					r.logger.Infof(r.ctx, "Reconnecting in write loop due to error: %v", err)
					if reconnectErr := r.reconnect(tr); reconnectErr != nil {
						writeOrDone(r.ctx, writeRes{err: fmt.Errorf("reconnect cause[%v]: %w", err, reconnectErr)}, r.writeResCh[data.id])
						return
					}
					continue
				}
				break
			}
			r.writeResMu.RLock()
			if ch, ok := r.writeResCh[data.id]; ok {
				writeOrDone(r.ctx, writeRes{}, ch)
			}
			r.writeResMu.RUnlock()
		}
	}
}

func (r *Transport) readLoop() {
	r.logger.Infof(r.ctx, "Starting read loop")
	if err := r.waitForConnection(r.ctx); err != nil {
		r.logger.Errorf(r.ctx, "Read loop canceled, failed to establish connection: %v", err)
		close(r.readResCh)
		return
	}

	defer close(r.readResCh)
	for {
		select {
		case <-r.ctx.Done():
			return
		default:
			r.mu.Lock()
			tr := r.transport
			r.mu.Unlock()

			// goroutine を使用してタイムアウト付きで読み取り
			readResultCh := make(chan struct {
				data []byte
				err  error
			}, 1)

			go func() {
				data, err := tr.Read()
				readResultCh <- struct {
					data []byte
					err  error
				}{data: data, err: err}
			}()

			select {
			case <-r.ctx.Done():
				return
			case <-time.After(r.readTimeout):
				// タイムアウト発生 - 再接続をトリガー
				transportID := r.negotiationParams.TransportID
				r.logger.Warnf(r.ctx, "[TransportID: %s] Read timeout (%v), attempting reconnect", transportID, r.readTimeout)
				if reconnectErr := r.reconnect(tr); reconnectErr != nil {
					r.logger.Errorf(r.ctx, "[TransportID: %s] Reconnect after timeout FAILED: %v", transportID, reconnectErr)
					writeOrDone(r.ctx, &readRes{err: fmt.Errorf("reconnect after timeout: %w", reconnectErr)}, r.readResCh)
					return
				}
				r.logger.Infof(r.ctx, "[TransportID: %s] Reconnect after timeout SUCCEEDED", transportID)
				continue
			case result := <-readResultCh:
				data, err := result.data, result.err
				if err != nil {
					if r.closed() {
						r.logger.Infof(r.ctx, "Read error while closed, exiting read loop")
						return
					}

					currentStatus := r.Status()
					r.logger.Infof(r.ctx, "Reconnecting in read loop due to error: %v (status before reconnect: %v)", err, currentStatus)
					if reconnectErr := r.reconnect(tr); reconnectErr != nil {
						r.logger.Errorf(r.ctx, "Reconnect FAILED: %v (final status: %v)", reconnectErr, r.Status())
						writeOrDone(r.ctx, &readRes{err: fmt.Errorf("reconnect cause[%v]: %w", err, reconnectErr)}, r.readResCh)
						return
					}
					r.logger.Infof(r.ctx, "Reconnect SUCCEEDED (new status: %v)", r.Status())
					continue
				}

				// コントロールメッセージ（ping/pong）としてデコードを試みる
				if msg, ok, err := TryParseControlMessage(data); err != nil {
					// プロトコルエラー - ログを記録して再接続をトリガー
					r.logger.Errorf(r.ctx, "Protocol error decoding control message: %v", err)
					if reconnectErr := r.reconnect(tr); reconnectErr != nil {
						r.logger.Errorf(r.ctx, "Reconnect after protocol error FAILED: %v", reconnectErr)
						writeOrDone(r.ctx, &readRes{err: fmt.Errorf("reconnect after protocol error: %w", reconnectErr)}, r.readResCh)
						return
					}
					continue
				} else if ok {
					// メッセージタイプに基づいて処理
					switch m := msg.(type) {
					case *PingMessage:
						// pong で自動応答（シーケンスをエコー）
						r.sendPong(m.Sequence)
					case *PongMessage:
						// RTT を計算して記録
						r.handlePongReceived(m.Sequence)
					}
					continue
				}

				// コントロールメッセージではない場合、上位層に渡す
				writeOrDone(r.ctx, &readRes{bs: data, err: nil}, r.readResCh)
			}
		}
	}
}

// AsUnreliable は、Transport を実装します。
func (r *Transport) AsUnreliable() (tr transport.UnreliableTransport, ok bool) {
	if err := r.waitForConnection(r.ctx); err != nil {
		r.logger.Warnf(r.ctx, "Failed to establish connection, cannot get AsUnreliable: %v", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.transport == nil {
		return nil, false
	}
	return r.transport.AsUnreliable()
}

// Close は、Transport を実装します。
func (r *Transport) Close() error {
	return r.CloseWithStatus(transport.CloseStatusNormal)
}

// CloseWithStatus は、指定されたステータスで下層のトランスポートをクローズします。
//
// Closer インターフェースを実装します。
func (r *Transport) CloseWithStatus(status transport.CloseStatus) error {
	r.cancel()
	r.mu.Lock()
	defer r.mu.Unlock()

	// noop にリセット（下層の Transport.Close() がメトリクスの Stop() を処理）
	r.metricsProvider = metrics.NewNopMetricsProvider()

	var err error
	// トランスポートが接続中の場合、r.transport は nil なので、まず nil をチェック
	if r.transport != nil {
		if c, ok := r.transport.(transport.Closer); ok {
			err = c.CloseWithStatus(status)
		}
	}

	r.statusMu.Lock()
	r.status = StatusDisconnected
	r.statusMu.Unlock()

	return err
}

// Name は、Transport を実装します。
func (r *Transport) Name() transport.Name {
	if err := r.waitForConnection(r.ctx); err != nil {
		r.logger.Warnf(r.ctx, "Failed to establish connection, cannot get Name: %v", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.transport == nil {
		return "" // または適切なデフォルト名
	}
	return r.transport.Name()
}

// NegotiationParams は、Transport を実装します。
func (r *Transport) NegotiationParams() transport.NegotiationParams {
	return r.negotiationParams
}

// Read は、Transport を実装します。
func (r *Transport) Read() ([]byte, error) {
	if err := r.waitForConnection(r.ctx); err != nil {
		return nil, fmt.Errorf("failed to establish initial connection for read: %w", err)
	}
	select {
	case <-r.ctx.Done():
		return nil, errors.ErrConnectionClosed
	case result, ok := <-r.readResCh:
		if !ok {
			return nil, errors.ErrConnectionClosed
		}
		return result.bs, result.err
	}
}

// RxBytesCounterValue は、Transport を実装します。
func (r *Transport) RxBytesCounterValue() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.transport == nil {
		return 0
	}
	return r.transport.RxBytesCounterValue()
}

// TxBytesCounterValue は、Transport を実装します。
func (r *Transport) TxBytesCounterValue() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.transport == nil {
		return 0
	}
	return r.transport.TxBytesCounterValue()
}

// Write は、Transport を実装します。
func (r *Transport) Write(data []byte) error {
	// waitForConnection は writeReqRes 内で呼び出される writeLoop 内で処理されるため、ここでは不要です。
	// writeReqRes がエラーを返す場合、接続試行の失敗が含まれる可能性があります。
	err := r.writeReqRes(data)
	if err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

// reconnect は、サーバーへの再接続を試みます。
//
// スレッドアンセーフなメソッドです。r.mu でロックされた状態で呼び出す必要があります。
func (r *Transport) reconnect(old transport.Transport) error {
	r.logger.Infof(r.ctx, "Reconnect called, acquiring lock...")
	r.mu.Lock()
	defer r.mu.Unlock()

	r.logger.Infof(r.ctx, "Lock acquired, changing status to StatusReconnecting")
	r.statusMu.Lock()
	r.status = StatusReconnecting
	r.statusMu.Unlock()

	if old != r.transport {
		// すでに再接続済み
		r.logger.Infof(r.ctx, "Already reconnected (old transport differs from current)")
		return nil
	}

	if r.closed() {
		r.logger.Infof(r.ctx, "Transport is closed, cannot reconnect")
		return errors.ErrConnectionClosed
	}

	r.logger.Infof(r.ctx, "Closing old transport...")
	if err := old.Close(); err != nil {
		r.logger.Infof(r.ctx, "Failed to close old transport: %v", err)
	} else {
		r.logger.Infof(r.ctx, "Old transport closed successfully")
	}

	// 再接続時に ping/pong の状態をリセット
	r.pingSeq.Store(0)
	r.pongSeqMu.Lock()
	r.pendingPongs = make(map[uint32]time.Time)
	r.pongSeqMu.Unlock()

	var rerr error
	for i := 0; ; i++ {
		if r.maxReconnectAttempts >= 0 && i >= r.maxReconnectAttempts {
			// すべての試行が失敗
			r.logger.Errorf(r.ctx, "All %d reconnect attempts failed, final error: %v", r.maxReconnectAttempts, rerr)
			return fmt.Errorf("reconnect: %w", rerr)
		}
		if r.closed() {
			r.logger.Infof(r.ctx, "Transport closed during reconnect attempts")
			return errors.ErrConnectionClosed
		}
		if r.maxReconnectAttempts < 0 {
			r.logger.Infof(r.ctx, "Attempting to reconnect (%d/unlimited)...", i+1)
		} else {
			r.logger.Infof(r.ctx, "Attempting to reconnect (%d/%d)...", i+1, r.maxReconnectAttempts)
		}
		startTime := time.Now()
		newTransport, err := r.reconnector.Connect()
		elapsed := time.Since(startTime)
		r.logger.Infof(r.ctx, "Connect() took %v", elapsed)
		if err != nil {
			rerr = err
			r.logger.Warnf(r.ctx, "Reconnect attempt %d failed: %v, sleeping %v...", i+1, err, r.reconnectInterval)
			time.Sleep(r.reconnectInterval)
			continue
		}

		r.logger.Infof(r.ctx, "Successfully reconnected on attempt %d, updating status to StatusConnected", i+1)
		r.transport = newTransport
		// 新しい接続のためにメトリクスプロバイダーを再初期化
		if ms, ok := newTransport.(transport.MetricsSupporter); ok {
			r.metricsProvider = ms.MetricsProvider()
		} else {
			r.metricsProvider = metrics.NewNopMetricsProvider()
		}
		r.statusMu.Lock()
		r.status = StatusConnected
		r.statusMu.Unlock()
		r.logger.Infof(r.ctx, "Status updated to StatusConnected, reconnect complete")
		return nil
	}
}

func (r *Transport) closed() bool {
	select {
	case <-r.ctx.Done():
		return true
	default:
		return false
	}
}

func (r *Transport) Status() Status {
	r.statusMu.RLock()
	defer r.statusMu.RUnlock()
	return r.status
}

// sendPong は、指定されたシーケンス番号で pong メッセージを送信します。
func (r *Transport) sendPong(seq uint32) {
	pongMsg, _ := (&PongMessage{Sequence: seq}).MarshalBinary()
	if err := r.writeReqRes(pongMsg); err != nil {
		r.logger.Errorf(r.ctx, "Failed to send pong (seq=%d): %v", seq, err)
	} else {
		r.logger.Debugf(r.ctx, "Sent pong (seq=%d)", seq)
	}
}

// handlePongReceived は、受信した pong メッセージを処理し、RTT を計算します。
func (r *Transport) handlePongReceived(seq uint32) {
	r.pongSeqMu.Lock()
	sendTime, exists := r.pendingPongs[seq]
	if !exists {
		r.pongSeqMu.Unlock()
		r.logger.Warnf(r.ctx, "Received pong with unexpected sequence: %d", seq)
		return
	}
	rtt := time.Since(sendTime)
	delete(r.pendingPongs, seq)
	r.pongSeqMu.Unlock()

	// アプリケーションRTTを更新
	r.appRTTMu.Lock()
	if r.rttSmoothingFactor >= 1.0 || r.appRTT == 0 {
		// スムージングなし、または初回
		r.appRTT = rtt
	} else {
		// 指数移動平均
		alpha := r.rttSmoothingFactor
		r.appRTT = time.Duration(float64(r.appRTT)*(1-alpha) + float64(rtt)*alpha)
	}
	smoothedRTT := r.appRTT
	r.appRTTMu.Unlock()

	r.logger.Debugf(r.ctx, "RTT: %v (seq=%d, smoothed: %v)", rtt, seq, smoothedRTT)
}

// RTT は、アプリケーションレベルRTT（利用可能な場合）またはメトリクスプロバイダーからのRTTを返します。
// アプリケーションRTTはPing/Pongメッセージから計算され、rttSmoothingFactorに基づいてスムージングされます。
func (r *Transport) RTT() time.Duration {
	switch r.rttSource {
	case RTTSourceApp:
		r.appRTTMu.RLock()
		appRTT := r.appRTT
		r.appRTTMu.RUnlock()
		return appRTT
	case RTTSourceMetrics:
		return r.metricsProvider.RTT()
	default: // RTTSourceAuto: TCP_INFO優先、利用不可時はPing/Pong
		if metricsRTT := r.metricsProvider.RTT(); metricsRTT > 0 {
			return metricsRTT
		}
		r.appRTTMu.RLock()
		appRTT := r.appRTT
		r.appRTTMu.RUnlock()
		return appRTT
	}
}

// RTTVar は、メトリクスプロバイダーから RTT 変動を返します。
func (r *Transport) RTTVar() time.Duration {
	return r.metricsProvider.RTTVar()
}

// CongestionWindow は、メトリクスプロバイダーから輻輳ウィンドウサイズを返します。
func (r *Transport) CongestionWindow() uint64 {
	return r.metricsProvider.CongestionWindow()
}

// BytesInFlight は、メトリクスプロバイダーから送信中のバイト数を返します。
func (r *Transport) BytesInFlight() uint64 {
	return r.metricsProvider.BytesInFlight()
}
