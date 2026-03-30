package reconnect

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/aptpod/iscp-go/v2/errors"
	"github.com/aptpod/iscp-go/v2/log"
	"github.com/aptpod/iscp-go/v2/transport"
	"github.com/aptpod/iscp-go/v2/transport/metrics"
)

var (
	_ transport.Transport = (*Transport)(nil)
)

type readRes struct {
	bs  []byte
	err error
}

type writeReq struct {
	bs    []byte
	resCh chan writeRes
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
//
// Lock ordering: mu must be acquired before statusMu.
// Callbacks from setStatus() must not call methods that acquire mu.
type Transport struct {
	reconnector          Connector
	transport            transport.Transport
	mu                   sync.RWMutex
	maxReconnectAttempts int
	reconnectInterval    time.Duration
	heartbeatInterval    time.Duration
	heartbeatTimeout     time.Duration

	readResCh  chan *readRes
	writeReqCh chan writeReq

	// useV4Protocol は v4 プロトコル機能（メッセージタイプバイト付加、ハートビート）を有効にするか。
	// TransportType が設定されている場合に true。v3 接続ではタイプバイト付加やハートビートを行わない。
	useV4Protocol bool

	// 最後にデータを送信した時刻（ハートビートループで使用）
	lastWriteTime atomic.Int64

	// トランスポートメトリクスプロバイダー（RTT、CWND など）
	// プロバイダーは Stop() を介して独自のライフサイクルを管理します。
	metricsProvider metrics.MetricsProvider

	ctx    context.Context
	cancel context.CancelFunc
	logger log.Logger

	statusMu       sync.RWMutex
	status         Status
	onStatusChange StatusChangeCallback

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
	c.DialConfig.SubConnectionID = dc.SubConnectionID
	return Dial(c)
}

// StatusChangeCallback は、接続ステータスが変更されたときに呼び出されるコールバック関数です。
type StatusChangeCallback func(oldStatus, newStatus Status)

// DialConfig は、再接続トランスポートの設定を保持します。
type DialConfig struct {
	Dialer               transport.Dialer
	DialConfig           transport.DialConfig
	MaxReconnectAttempts int
	ReconnectInterval    time.Duration
	HeartbeatInterval    time.Duration
	HeartbeatTimeout     time.Duration
	Logger               log.Logger

	// OnStatusChange は、接続ステータスが変更されたときに呼び出されるコールバックです。
	// 切断検知時に上位層への通知を行うために使用します。
	OnStatusChange StatusChangeCallback
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

	// ネゴシエーションパラメータを取得し、利用可能な場合はハートビート間隔/タイムアウトを設定
	negParams := c.DialConfig.NegotiationParams()

	// ハートビート間隔を設定: ネゴシエーションパラメータ、次に設定、最後にデフォルトの優先順位
	heartbeatInterval := c.HeartbeatInterval
	if negParams.HeartbeatInterval != nil && *negParams.HeartbeatInterval > 0 {
		heartbeatInterval = time.Duration(*negParams.HeartbeatInterval) * time.Second
	} else if heartbeatInterval == 0 {
		heartbeatInterval = 10 * time.Second
	}

	// ハートビートタイムアウトを設定: ネゴシエーションパラメータ、次に設定、最後にデフォルトの優先順位
	heartbeatTimeout := c.HeartbeatTimeout
	if negParams.HeartbeatTimeout != nil && *negParams.HeartbeatTimeout > 0 {
		heartbeatTimeout = time.Duration(*negParams.HeartbeatTimeout) * time.Second
	} else if heartbeatTimeout == 0 {
		heartbeatTimeout = 30 * time.Second
	}

	if c.Logger == nil {
		c.Logger = log.NewNop()
	}
	if c.DialConfig.SubConnectionID == "" {
		c.DialConfig.SubConnectionID = transport.SubConnectionID(uuid.New().String())
	}

	// まだ設定されていない場合、ハートビート間隔とタイムアウトをネゴシエーションパラメータに設定
	if negParams.HeartbeatInterval == nil {
		intervalSec := int(heartbeatInterval.Seconds())
		negParams.HeartbeatInterval = &intervalSec
	}
	if negParams.HeartbeatTimeout == nil {
		timeoutSec := int(heartbeatTimeout.Seconds())
		negParams.HeartbeatTimeout = &timeoutSec
	}

	// 再接続パラメータをネゴシエーションパラメータに設定
	if negParams.MaxReconnectAttempts == nil {
		negParams.MaxReconnectAttempts = &c.MaxReconnectAttempts
	}
	if negParams.ReconnectInterval == nil {
		reconnectIntervalSec := int(c.ReconnectInterval.Seconds())
		negParams.ReconnectInterval = &reconnectIntervalSec
	}

	// DialConfigに再接続パラメータを反映（子ダイアラーのネゴシエーションに含めるため）
	c.DialConfig.MaxReconnectAttempts = negParams.MaxReconnectAttempts
	c.DialConfig.ReconnectInterval = negParams.ReconnectInterval
	c.DialConfig.HeartbeatInterval = negParams.HeartbeatInterval
	c.DialConfig.HeartbeatTimeout = negParams.HeartbeatTimeout

	// まずTransportインスタンスを作成し、実際の接続はバックグラウンドで実行

	// v4 プロトコル機能は TransportType が設定されている場合のみ有効
	useV4 := c.DialConfig.TransportType != ""

	t := &Transport{
		reconnector: TransportConnectorFunc(func() (transport.Transport, error) {
			return c.Dialer.Dial(c.DialConfig)
		}),
		transport:            nil, // 初期状態では内部トランスポートは nil
		mu:                   sync.RWMutex{},
		useV4Protocol:        useV4,
		maxReconnectAttempts: c.MaxReconnectAttempts,
		reconnectInterval:    c.ReconnectInterval,
		heartbeatInterval:    heartbeatInterval,
		heartbeatTimeout:     heartbeatTimeout,
		readResCh:            make(chan *readRes, 1024),
		writeReqCh:           make(chan writeReq, 1024),
		metricsProvider:      metrics.NewNopMetricsProvider(), // noop で初期化
		ctx:                  nil,
		cancel:               nil,
		logger:               c.Logger,
		statusMu:             sync.RWMutex{},
		status:               StatusConnecting, // 新しい "connecting" ステータス
		onStatusChange:       c.OnStatusChange,
		initialConnectDoneCh: make(chan error, 1),
		negotiationParams:    negParams,
	}
	t.ctx, t.cancel = context.WithCancel(context.Background())

	// バックグラウンドで接続プロセスを実行
	go t.initialConnect(c.Dialer, c.DialConfig)

	if t.useV4Protocol {
		go t.heartbeatLoop()
	}
	go t.readLoop()
	go t.writeLoop()
	return t, nil
}

// initialConnect は、バックグラウンドで初期接続試行を実行します。
func (r *Transport) initialConnect(dialer transport.Dialer, dialConfig transport.DialConfig) {
	r.logger.Infof(r.ctx, "Starting initial connection attempts...")
	var err error

	doneProcess := func(err error, status Status) {
		r.setStatus(status)
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

func (r *Transport) heartbeatLoop() {
	r.logger.Infof(r.ctx, "Starting heartbeat loop")
	if err := r.waitForConnection(r.ctx); err != nil {
		r.logger.Errorf(r.ctx, "Heartbeat loop canceled, failed to establish connection: %v", err)
		return
	}

	ticker := time.NewTicker(r.heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.ctx.Done():
			r.logger.Infof(r.ctx, "Heartbeat loop stopped")
			return
		case <-ticker.C:
			// lastWriteTime を確認し、ハートビート間隔内にデータ送信があった場合はスキップ
			lastWrite := time.Unix(0, r.lastWriteTime.Load())
			if time.Since(lastWrite) < r.heartbeatInterval {
				continue
			}

			heartbeat, _ := (&HeartbeatMessage{}).MarshalBinary()
			if err := r.writeRaw(heartbeat); err != nil {
				r.logger.Errorf(r.ctx, "Failed to send heartbeat: %v", err)
				return
			}
			r.lastWriteTime.Store(time.Now().UnixNano())
			r.logger.Debugf(r.ctx, "Sent heartbeat")
		}
	}
}

func (r *Transport) writeReqRes(bs []byte) error {
	resCh := make(chan writeRes, 1)

	writeOrDone(r.ctx, writeReq{bs: bs, resCh: resCh}, r.writeReqCh)

	res, ok := readOrDoneOne(r.ctx, resCh)
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
					writeOrDone(r.ctx, writeRes{err: fmt.Errorf("failed to establish initial connection: %w", err)}, data.resCh)
					continue
				}
				// waitForConnection 後に trEstablished を再度チェック
				r.mu.RLock()
				trEstablished = r.transport != nil
				r.mu.RUnlock()
				if !trEstablished { // それでもまだ確立されていない場合はエラー
					writeOrDone(r.ctx, writeRes{err: errors.New("transport not connected after wait")}, data.resCh)
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
					// 正常クローズの場合は再接続せず終了
					if transport.IsNormalClose(err) {
						r.logger.Infof(r.ctx, "Write loop: normal close detected, exiting without reconnect")
						writeOrDone(r.ctx, writeRes{err: err}, data.resCh)
						return
					}
					r.logger.Infof(r.ctx, "Reconnecting in write loop due to error: %v", err)
					if reconnectErr := r.reconnect(tr); reconnectErr != nil {
						writeOrDone(r.ctx, writeRes{err: fmt.Errorf("reconnect cause[%v]: %w", err, reconnectErr)}, data.resCh)
						return
					}
					continue
				}
				break
			}
			// 書き込み成功後に lastWriteTime を更新（ハートビートループで使用）
			if r.useV4Protocol {
				r.lastWriteTime.Store(time.Now().UnixNano())
			}
			writeOrDone(r.ctx, writeRes{}, data.resCh)
		}
	}
}

// readResult は、永続リーダー goroutine から readLoop への読み取り結果です。
type readResult struct {
	data []byte
	err  error
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
		r.mu.RLock()
		tr := r.transport
		r.mu.RUnlock()
		if tr == nil {
			return
		}

		// 現在のトランスポートに対して永続リーダー goroutine を起動
		readCh := make(chan readResult, 1)
		readerDone := make(chan struct{})
		go func() {
			defer close(readerDone)
			for {
				data, err := tr.Read()
				select {
				case readCh <- readResult{data: data, err: err}:
				case <-r.ctx.Done():
					return
				}
				if err != nil {
					return
				}
			}
		}()

		// 読み取り結果を処理。再接続が必要な場合は errNeedReconnect を返す
		needReconnect, readErr := r.processReads(readCh, tr)
		// リーダー goroutine の終了を待機（reconnect で tr が Close されるため必ず終了する）
		<-readerDone

		if !needReconnect {
			if readErr != nil {
				writeOrDone(r.ctx, &readRes{err: readErr}, r.readResCh)
			}
			return
		}
		// needReconnect == true: 新しいトランスポートで次のイテレーションへ
		continue
	}
}

// processReads は永続リーダー goroutine からの読み取り結果を処理します。
// 再接続が必要な場合は (true, nil) を返します。
// 致命的エラーまたは正常終了の場合は (false, err) を返します。
func (r *Transport) processReads(readCh <-chan readResult, tr transport.Transport) (needReconnect bool, fatalErr error) {
	// v4ではハートビートタイムアウトを使用、v3ではタイムアウトなし
	var heartbeatTimer *time.Timer
	var timerC <-chan time.Time
	if r.useV4Protocol {
		heartbeatTimer = time.NewTimer(r.heartbeatTimeout)
		defer heartbeatTimer.Stop()
		timerC = heartbeatTimer.C
	}

	for {
		select {
		case <-r.ctx.Done():
			return false, nil

		case <-timerC:
			// ハートビートタイムアウト発生
			transportID := r.negotiationParams.SubConnectionID
			r.logger.Warnf(r.ctx, "[SubConnectionID: %s] Read timeout (%v), attempting reconnect", transportID, r.heartbeatTimeout)
			if reconnectErr := r.reconnect(tr); reconnectErr != nil {
				r.logger.Errorf(r.ctx, "[SubConnectionID: %s] Reconnect after timeout FAILED: %v", transportID, reconnectErr)
				return false, fmt.Errorf("reconnect after timeout: %w", reconnectErr)
			}
			r.logger.Infof(r.ctx, "[SubConnectionID: %s] Reconnect after timeout SUCCEEDED", transportID)
			return true, nil

		case result := <-readCh:
			if result.err != nil {
				if r.closed() {
					r.logger.Infof(r.ctx, "Read error while closed, exiting read loop")
					return false, nil
				}
				// 正常クローズの場合は再接続せず終了
				if transport.IsNormalClose(result.err) {
					r.logger.Infof(r.ctx, "Read loop: normal close detected, exiting without reconnect")
					r.cancel() // writeLoop/heartbeatLoop に NormalClose を伝播し再接続を抑止
					return false, result.err
				}

				currentStatus := r.Status()
				r.logger.Infof(r.ctx, "Reconnecting in read loop due to error: %v (status before reconnect: %v)", result.err, currentStatus)
				if reconnectErr := r.reconnect(tr); reconnectErr != nil {
					r.logger.Errorf(r.ctx, "Reconnect FAILED: %v (final status: %v)", reconnectErr, r.Status())
					return false, fmt.Errorf("reconnect cause[%v]: %w", result.err, reconnectErr)
				}
				r.logger.Infof(r.ctx, "Reconnect SUCCEEDED (new status: %v)", r.Status())
				return true, nil
			}

			// ハートビートタイマーをリセット（データ受信のたびに）
			if heartbeatTimer != nil {
				if !heartbeatTimer.Stop() {
					select {
					case <-heartbeatTimer.C:
					default:
					}
				}
				heartbeatTimer.Reset(r.heartbeatTimeout)
			}

			// v3: データをそのまま上位層に渡す
			if !r.useV4Protocol {
				writeOrDone(r.ctx, &readRes{bs: result.data, err: nil}, r.readResCh)
				continue
			}

			// v4: メッセージタイプバイトを解析
			msgType, parseErr := ParseMessageType(result.data)
			if parseErr != nil {
				// プロトコルエラー - ログを記録して再接続をトリガー
				r.logger.Errorf(r.ctx, "Protocol error parsing message type: %v", parseErr)
				if reconnectErr := r.reconnect(tr); reconnectErr != nil {
					r.logger.Errorf(r.ctx, "Reconnect after protocol error FAILED: %v", reconnectErr)
					return false, fmt.Errorf("reconnect after protocol error: %w", reconnectErr)
				}
				return true, nil
			}

			switch msgType {
			case MessageTypeHeartbeat:
				r.logger.Debugf(r.ctx, "Received heartbeat")
				continue
			case MessageTypeISCP:
				// iSCPメッセージ - 先頭のタイプバイトを除去して上位層に渡す
				writeOrDone(r.ctx, &readRes{bs: result.data[1:], err: nil}, r.readResCh)
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
		err = r.transport.CloseWithStatus(status)
	}

	r.setStatus(StatusDisconnected)

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
	var payload []byte
	if r.useV4Protocol {
		// v4: 0x00 タイプバイトを先頭に付加
		payload = make([]byte, len(data)+1)
		payload[0] = byte(MessageTypeISCP)
		copy(payload[1:], data)
	} else {
		// v3: そのまま送信
		payload = data
	}

	err := r.writeReqRes(payload)
	if err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

// writeRaw はタイプバイト付加なしでデータを送信します（ハートビート用）。
func (r *Transport) writeRaw(data []byte) error {
	return r.writeReqRes(data)
}

// reconnect は、サーバーへの再接続を試みます。
// 内部で r.mu を取得するため、呼び出し元でロックを保持してはいけません。
func (r *Transport) reconnect(old transport.Transport) error {
	r.logger.Infof(r.ctx, "Reconnect called, acquiring lock...")
	r.mu.Lock()
	defer r.mu.Unlock()

	// CloseWithStatus が呼ばれた場合、再接続せず即座に終了
	if r.closed() {
		r.logger.Infof(r.ctx, "Transport is closed, skipping reconnect")
		return errors.ErrConnectionClosed
	}

	r.logger.Infof(r.ctx, "Lock acquired, changing status to StatusReconnecting")
	r.setStatus(StatusReconnecting)

	if old != r.transport {
		// すでに再接続済み
		r.logger.Infof(r.ctx, "Already reconnected (old transport differs from current)")
		return nil
	}

	r.logger.Infof(r.ctx, "Closing old transport...")
	if err := old.Close(); err != nil {
		r.logger.Infof(r.ctx, "Failed to close old transport: %v", err)
	} else {
		r.logger.Infof(r.ctx, "Old transport closed successfully")
	}

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
		r.setStatus(StatusConnected)
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

// SetOnStatusChange sets a callback function that is called when the transport status changes.
// This can be called after Dial() to set or replace the callback.
// The callback is invoked synchronously within the status change, so it should not block.
func (r *Transport) SetOnStatusChange(cb StatusChangeCallback) {
	r.statusMu.Lock()
	defer r.statusMu.Unlock()
	r.onStatusChange = cb
}

// setStatus は、ステータスを変更し、コールバックが設定されている場合は通知します。
func (r *Transport) setStatus(newStatus Status) {
	r.statusMu.Lock()
	oldStatus := r.status
	r.status = newStatus
	cb := r.onStatusChange
	r.statusMu.Unlock()

	if oldStatus != newStatus && cb != nil {
		cb(oldStatus, newStatus)
	}
}

// RTT は、メトリクスプロバイダーからのRTTを返します。
func (r *Transport) RTT() time.Duration {
	return r.metricsProvider.RTT()
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
