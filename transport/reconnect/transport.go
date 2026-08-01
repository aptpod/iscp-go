package reconnect

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/aptpod/iscp-go/v2/errors"
	"github.com/aptpod/iscp-go/v2/internal/ch"
	"github.com/aptpod/iscp-go/v2/log"
	"github.com/aptpod/iscp-go/v2/transport"
	"github.com/aptpod/iscp-go/v2/transport/metrics"
)

var (
	_ transport.Transport = (*Transport)(nil)
)

// ErrNotConnected は Write 呼び出し時点でトランスポートが書き込み可能状態に無かったため、
// データが一切送信されなかったことを示すセンチネルエラー。
// multi.Transport はこのエラーのみ別 sub-conn へのフォールバック対象とする。
// 下層 transport.Write 自体が返したエラー（部分送信の可能性あり）はこのセンチネルで包まない。
var ErrNotConnected = errors.New("reconnect transport: not connected")

type readRes struct {
	bs  []byte
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
//
// Write 側は writeMu のみで排他され、下層 transport.Write を同期呼び出しする。
// reconnect() は reconnectMu で直列化し、Connect() リトライ中は r.mu を
// 保持しない（writeRaw 等が下層スナップショットを取得できるようにするため）。
type Transport struct {
	reconnector          Connector
	transport            transport.Transport
	mu                   sync.RWMutex
	maxReconnectAttempts int
	reconnectInterval    time.Duration
	heartbeatInterval    time.Duration
	heartbeatTimeout     time.Duration

	readResCh chan *readRes

	// writeMu は下層 transport.Write の排他に使用する。
	// 下層実装（gorilla/websocket など）の並行書き込み制約を満たすため。
	writeMu sync.Mutex

	// reconnectMu は reconnect() 呼び出しを直列化する。
	// processReads からの同期呼び出しは Lock で待機し、Write 失敗経由の
	// triggerReconnect は TryLock で早期 bail する（重複起動抑止）。
	reconnectMu sync.Mutex

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
	Dialer     transport.Dialer
	DialConfig transport.DialConfig
	// MaxReconnectAttemptsは、再接続を試行する最大回数です。
	// 0を指定すると既定値30が使われます。負値は無制限リトライを意味し、
	// 接続が回復しない限り再接続ループは終了しないため、Dial / Writeは返りません（意図された仕様です）。
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
			// 再接続中などで Connected でない場合はハートビートをスキップ
			if r.Status() != StatusConnected {
				continue
			}
			// lastWriteTime を確認し、ハートビート間隔内にデータ送信があった場合はスキップ
			lastWrite := time.Unix(0, r.lastWriteTime.Load())
			if time.Since(lastWrite) < r.heartbeatInterval {
				continue
			}

			heartbeat, _ := (&HeartbeatMessage{}).MarshalBinary()
			if err := r.writeRaw(heartbeat); err != nil {
				// 再接続トリガ済みなのでハートビート失敗は非致命扱いで継続
				r.logger.Warnf(r.ctx, "Failed to send heartbeat: %v", err)
				continue
			}
			r.lastWriteTime.Store(time.Now().UnixNano())
			r.logger.Debugf(r.ctx, "Sent heartbeat")
		}
	}
}

// writeRaw はタイプバイト付加なしで下層 transport.Write を同期的に呼び出します。
// 書き込みエラー時は非同期 reconnect を 1 回だけトリガします。
func (r *Transport) writeRaw(data []byte) error {
	// 状態待機は writeMu の外で行う。writeMu は下層 tr.Write の直列化専用。
	if err := r.waitForWritable(); err != nil {
		return err
	}

	r.writeMu.Lock()
	defer r.writeMu.Unlock()

	r.mu.RLock()
	tr := r.transport
	r.mu.RUnlock()
	if tr == nil {
		return fmt.Errorf("%w: transport is nil", ErrNotConnected)
	}

	if err := tr.Write(data); err != nil {
		if !r.closed() && !transport.IsNormalClose(err) {
			go r.triggerReconnect(tr)
		}
		// tr.Write 自体が返したエラーは部分送信の可能性があるため ErrNotConnected で包まない
		return err
	}
	return nil
}

// waitForWritable は Write 可能な状態になるまで待機します。
// 返したエラーは全て ErrNotConnected で包まれ、下層 tr.Write を呼ぶ前に
// 失敗したことを呼び出し元に伝える（multi.Transport のフォールバック対象）。
//
//   - StatusConnecting: 初期接続完了を待機（Dial 直後の Write 互換）。
//   - StatusReconnecting: 有限リトライなら完了まで待機。無制限リトライなら
//     即エラー（multi.Transport のフォールバックを妨げないため）。
//   - StatusDisconnected: 即エラー。
func (r *Transport) waitForWritable() error {
	for {
		if r.closed() {
			return fmt.Errorf("%w: %w", ErrNotConnected, errors.ErrConnectionClosed)
		}
		switch status := r.Status(); status {
		case StatusConnected:
			return nil
		case StatusConnecting:
			if err := r.waitForConnection(r.ctx); err != nil {
				return fmt.Errorf("%w: wait for connection: %w", ErrNotConnected, err)
			}
		case StatusReconnecting:
			if r.maxReconnectAttempts < 0 {
				return fmt.Errorf("%w: reconnecting with unlimited retry", ErrNotConnected)
			}
			select {
			case <-r.ctx.Done():
				return fmt.Errorf("%w: %w", ErrNotConnected, errors.ErrConnectionClosed)
			case <-time.After(10 * time.Millisecond):
			}
		case StatusDisconnected:
			return fmt.Errorf("%w: status=disconnected", ErrNotConnected)
		default:
			return fmt.Errorf("%w: unknown status %v", ErrNotConnected, status)
		}
	}
}

// waitForReconnectToFinish は進行中の reconnect goroutine の完了を待機します。
// reconnectMu を取得→解放するだけですが、lock 取得成功時点で in-flight な
// doReconnect は抜けていることが保証されます。Close から呼び出されます。
func (r *Transport) waitForReconnectToFinish() {
	r.reconnectMu.Lock()
	//nolint:staticcheck // SA2001: 進行中 reconnect の完了を待つための Lock/Unlock ペア
	r.reconnectMu.Unlock()
}

// triggerReconnect は reconnect() を非同期に起動します。
// 既に reconnect が走っていれば TryLock で早期 bail し重複起動を抑止します。
func (r *Transport) triggerReconnect(old transport.Transport) {
	if !r.reconnectMu.TryLock() {
		return
	}
	defer r.reconnectMu.Unlock()
	if err := r.doReconnect(old); err != nil {
		r.logger.Errorf(r.ctx, "Triggered reconnect failed: %v", err)
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
				ch.WriteOrDone(r.ctx, &readRes{err: readErr}, r.readResCh)
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
				ch.WriteOrDone(r.ctx, &readRes{bs: result.data, err: nil}, r.readResCh)
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
				ch.WriteOrDone(r.ctx, &readRes{bs: result.data[1:], err: nil}, r.readResCh)
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
//
// 進行中の reconnect goroutine（processReads 経由で起動、または triggerReconnect
// による非同期起動）が残留しないよう、reconnectMu を取得して完了を待機する。
// Connect() 自体は ctx を honor しないため、Dialer 内部のタイムアウトまで
// ブロックする可能性がある。
func (r *Transport) CloseWithStatus(status transport.CloseStatus) error {
	r.cancel()

	// 進行中の reconnect が ctx キャンセルを観測して終了するまで待機。
	// reconnectMu を保持できた時点でいずれの reconnect goroutine も doReconnect を抜けている。
	r.waitForReconnectToFinish()

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
//
// 下層 transport.Write を同期呼び出しし、エラー時は即座に呼び出し元へ返します。
// 再接続中や未接続の場合はエラーを即返して呼び出し元（multi.Transport 等）の
// フォールバックに委ねます。
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

	if err := r.writeRaw(payload); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	if r.useV4Protocol {
		r.lastWriteTime.Store(time.Now().UnixNano())
	}
	return nil
}

// reconnect は、サーバーへの再接続を試みます。
//
// 呼び出しは reconnectMu で直列化されます。processReads 等からの同期呼び出しは
// Lock で待機し、triggerReconnect（Write 失敗経由の非同期起動）は TryLock で
// 早期 bail します。
func (r *Transport) reconnect(old transport.Transport) error {
	r.reconnectMu.Lock()
	defer r.reconnectMu.Unlock()
	return r.doReconnect(old)
}

// doReconnect は reconnectMu を既に保持している呼び出し元から起動されます。
// Connect() リトライ中は r.mu を保持せず、writeRaw や readLoop が下層
// スナップショットを取得できるようにします（旧 Closed 状態の tr を掴んだ場合は
// Write/Read が即エラーを返すため、呼び出し元のフォールバックが機能します）。
func (r *Transport) doReconnect(old transport.Transport) error {
	// 先行の reconnect が既に完了していたら no-op
	r.mu.RLock()
	currentTr := r.transport
	r.mu.RUnlock()
	if currentTr != old {
		r.logger.Infof(r.ctx, "Already reconnected (old transport differs from current)")
		return nil
	}

	if r.closed() {
		return errors.ErrConnectionClosed
	}

	r.setStatus(StatusReconnecting)
	r.logger.Infof(r.ctx, "Closing old transport...")
	if err := old.Close(); err != nil {
		r.logger.Infof(r.ctx, "Failed to close old transport: %v", err)
	}

	var rerr error
	for i := 0; ; i++ {
		if r.maxReconnectAttempts >= 0 && i >= r.maxReconnectAttempts {
			r.logger.Errorf(r.ctx, "All %d reconnect attempts failed, final error: %v", r.maxReconnectAttempts, rerr)
			// 再接続を諦めた時点で Disconnected に遷移させる。
			// これにより waitForWritable が永久ポーリングしないで Write が即エラー返却する。
			r.setStatus(StatusDisconnected)
			return fmt.Errorf("reconnect: %w", rerr)
		}
		if r.closed() {
			r.setStatus(StatusDisconnected)
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

		r.mu.Lock()
		r.transport = newTransport
		if ms, ok := newTransport.(transport.MetricsSupporter); ok {
			r.metricsProvider = ms.MetricsProvider()
		} else {
			r.metricsProvider = metrics.NewNopMetricsProvider()
		}
		r.mu.Unlock()
		r.setStatus(StatusConnected)
		r.logger.Infof(r.ctx, "Successfully reconnected on attempt %d", i+1)
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

// currentMetricsProvider は、現在のメトリクスプロバイダーを r.mu 保護下で取得します。
// reconnect() が r.metricsProvider を差し替える可能性があるため、読み取りにもロックが必要です。
func (r *Transport) currentMetricsProvider() metrics.MetricsProvider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.metricsProvider
}

// RTT は、メトリクスプロバイダーからのRTTを返します。
func (r *Transport) RTT() time.Duration {
	return r.currentMetricsProvider().RTT()
}

// RTTVar は、メトリクスプロバイダーから RTT 変動を返します。
func (r *Transport) RTTVar() time.Duration {
	return r.currentMetricsProvider().RTTVar()
}

// CongestionWindow は、メトリクスプロバイダーから輻輳ウィンドウサイズを返します。
func (r *Transport) CongestionWindow() uint64 {
	return r.currentMetricsProvider().CongestionWindow()
}

// BytesInFlight は、メトリクスプロバイダーから送信中のバイト数を返します。
func (r *Transport) BytesInFlight() uint64 {
	return r.currentMetricsProvider().BytesInFlight()
}
