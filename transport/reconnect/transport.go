package reconnect

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aptpod/iscp-go/errors"
	"github.com/aptpod/iscp-go/log"
	"github.com/aptpod/iscp-go/transport"
	"github.com/google/uuid"
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

type Connector interface {
	Connect() (transport.Transport, error)
}

type TransportConnectorFunc func() (transport.Transport, error)

func (f TransportConnectorFunc) Connect() (transport.Transport, error) {
	return f()
}

type (
	Ping struct{}
	Pong struct{}
)

var (
	PingMessage = []byte("ping")
	PongMessage = []byte("pong")
)

func IsPing(bs []byte) bool {
	return bytes.Equal(bs, PingMessage)
}

func IsPong(bs []byte) bool {
	return bytes.Equal(bs, PongMessage)
}

type Transport struct {
	reconnector          Connector
	transport            transport.Transport
	mu                   sync.RWMutex
	maxReconnectAttempts int
	reconnectInterval    time.Duration

	readResCh chan *readRes
	pongCh    chan Pong
	pingCh    chan Ping
	resPongCh chan Pong

	writeID    atomic.Int64
	writeReqCh chan writeReq
	writeResMu sync.RWMutex
	writeResCh map[int64]chan writeRes

	ctx    context.Context
	cancel context.CancelFunc
	logger log.Logger

	statusMu sync.RWMutex
	status   Status

	initialConnectDoneCh chan error
	initialConnectOnce   sync.Once
}

type Dialer struct {
	DialConfig *DialConfig
}

func NewDialer(c *DialConfig) *Dialer {
	return &Dialer{DialConfig: c}
}

func (d *Dialer) Dial(dc transport.DialConfig) (transport.Transport, error) {
	c := *d.DialConfig
	c.DialConfig.TransportID = dc.TransportID
	return Dial(c)
}

type DialConfig struct {
	Dialer               transport.Dialer
	DialConfig           transport.DialConfig
	MaxReconnectAttempts int
	ReconnectInterval    time.Duration
	Logger               log.Logger
}

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
	if c.Logger == nil {
		c.Logger = log.NewNop()
	}
	if c.DialConfig.TransportID == "" {
		c.DialConfig.TransportID = transport.TransportID(uuid.New().String())
	}

	// Transportインスタンスを先に作成し、実際の接続はバックグラウンドで行う

	t := &Transport{
		reconnector: TransportConnectorFunc(func() (transport.Transport, error) {
			cc := c.DialConfig
			cc.Reconnect = true
			return c.Dialer.Dial(cc)
		}),
		transport:            nil, // 初期状態では内部トランスポートはnil
		mu:                   sync.RWMutex{},
		maxReconnectAttempts: c.MaxReconnectAttempts,
		reconnectInterval:    c.ReconnectInterval,
		readResCh:            make(chan *readRes, 1024),
		pongCh:               make(chan Pong, 8),
		pingCh:               make(chan Ping, 8),
		resPongCh:            make(chan Pong, 8),
		writeReqCh:           make(chan writeReq, 1024),
		writeResCh:           make(map[int64]chan writeRes),
		ctx:                  nil,
		cancel:               nil,
		logger:               c.Logger,
		statusMu:             sync.RWMutex{},
		status:               StatusConnecting, // 新しい「接続中」ステータス
		initialConnectDoneCh: make(chan error, 1),
	}
	t.ctx, t.cancel = context.WithCancel(context.Background())

	// 接続処理をバックグラウンドで実行
	go t.initialConnect(c.Dialer, c.DialConfig)

	go t.pingLoop()
	go t.readLoop()
	go t.writeLoop()
	return t, nil
}

// initialConnect は、バックグラウンドで最初の接続試行を行います。
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
			r.cancel() // Transport全体を終了させる
		}
	}

	for i := range r.maxReconnectAttempts {
		if r.closed() {
			r.logger.Infof(r.ctx, "Initial connection canceled.")
			doneProcess(errors.ErrConnectionClosed, StatusDisconnected)
			return
		}
		r.logger.Infof(r.ctx, "Attempting to connect (%d/%d)...", i+1, r.maxReconnectAttempts)
		currentTr, currentErr := dialer.Dial(dialConfig) // ループ内の一時的なエラー変数
		err = currentErr                                 // 最終エラーを更新
		if currentErr == nil {
			if _, ok := currentTr.(transport.Closer); !ok {
				err = fmt.Errorf("transport does not implement Closer")
				r.logger.Errorf(r.ctx, "Initial connection failed as transport does not implement Closer: %v", err)
				doneProcess(err, StatusDisconnected)
				return
			}
			r.mu.Lock()
			r.transport = currentTr
			r.mu.Unlock()
			doneProcess(nil, StatusConnected) // 接続成功時にステータスを更新
			r.logger.Infof(r.ctx, "Successfully connected.")
			return
		}
		r.logger.Warnf(r.ctx, "Initial connection attempt failed: %v", currentErr)
		time.Sleep(r.reconnectInterval)
	}
	// 全ての試行が失敗した場合
	doneProcess(err, StatusDisconnected) // ステータスを更新
}

// waitForConnection は、初期接続が完了するか、コンテキストがキャンセルされるまで待機します。
// 接続が成功した場合はnilを、失敗した場合はエラーを返します。
func (r *Transport) waitForConnection(ctx context.Context) error {
	currentStatus := r.Status()
	if currentStatus == StatusConnected {
		return nil
	}
	if currentStatus == StatusDisconnected && !r.closed() { // closed() は ctx.Done() を見るので、ここでは status のみで判断
		return errors.New("transport is disconnected")
	}

	if currentStatus == StatusConnecting {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.ctx.Done(): // Transport自体のコンテキストも監視
			return errors.ErrConnectionClosed
		case err, ok := <-r.initialConnectDoneCh:
			if !ok {
				// チャネルが既にクローズされている場合 (initialConnect が完了済み)
				// この時点でのステータスを信頼する
				if r.Status() == StatusConnected {
					return nil
				}
				return errors.New("initial connection previously failed or channel closed unexpectedly")
			}
			// initialConnectDoneCh から値を受信 (initialConnect が今完了した)
			if err != nil {
				// initialConnect がエラーで完了
				return fmt.Errorf("initial connection attempt failed: %w", err)
			}
			// initialConnect が成功で完了 (err == nil)
			if r.Status() != StatusConnected {
				// 通知は成功だが、何らかの理由でステータスが一致しない (レースコンディションなど考えにくいが念のため)
				return errors.New("connection status inconsistent after initial connect success notification")
			}
			return nil
		}
	}
	// StatusReconnecting の場合は、ここでは待機せず、各操作で再接続処理に任せる
	// StatusDisconnected の場合は、既に上で処理されているか、または initialConnect が完了して失敗した結果。
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
	// r.closed() のチェックは waitForConnection 内およびループの select で行われるため、ここでは不要

	for range readOrDone(r.ctx, r.pingCh) {
		if err := r.writeReqRes(PongMessage); err != nil {
			r.logger.Errorf(r.ctx, "Failed to write pong: %v", err)
			return
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
	// no need to close writeCh
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
				// 内部トランスポートがまだ確立されていない場合、接続を待機する
				if err := r.waitForConnection(r.ctx); err != nil {
					writeOrDone(r.ctx, writeRes{err: fmt.Errorf("failed to establish initial connection: %w", err)}, r.writeResCh[data.id])
					continue
				}
				// waitForConnection 後に再度 trEstablished を確認
				r.mu.RLock()
				trEstablished = r.transport != nil
				r.mu.RUnlock()
				if !trEstablished { // それでも確立されていなければエラー
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
			data, err := tr.Read()
			if err != nil {
				if r.closed() {
					return
				}
				if errors.Is(err, errors.ErrConnectionNormalClose) {
					r.logger.Infof(r.ctx, "Normal close message received from server. Closing transport.")
					if err := r.CloseWithStatus(transport.CloseStatusNormal); err != nil {
						r.logger.Warnf(r.ctx, "Error while closing transport after normal close: %v", err)
					}
					return
				}

				r.logger.Infof(r.ctx, "Reconnecting in read loop due to error: %v", err)
				if reconnectErr := r.reconnect(tr); reconnectErr != nil {
					writeOrDone(r.ctx, &readRes{err: fmt.Errorf("reconnect cause[%v]: %w", err, reconnectErr)}, r.readResCh)
					return
				}
				continue
			}
			switch {
			case IsPing(data):
				writeOrDone(r.ctx, Ping{}, r.pingCh)
				continue
			}
			writeOrDone(r.ctx, &readRes{bs: data, err: nil}, r.readResCh)
		}
	}
}

// AsUnreliable implements Transport.
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

// Close implements Transport.
func (r *Transport) Close() error {
	return r.CloseWithStatus(transport.CloseStatusNormal)
}

// CloseWithStatus closes the underlying transport with the given status.
//
// It implements the Closer interface.
func (r *Transport) CloseWithStatus(status transport.CloseStatus) error {
	r.cancel()
	r.mu.Lock()
	defer r.mu.Unlock()
	var err error
	if c, ok := r.transport.(transport.Closer); ok {
		err = c.CloseWithStatus(status)
	} else {
		// fallback
		panic("implement closer")
	}
	r.status = StatusDisconnected
	return err
}

// Name implements Transport.
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

// NegotiationParams implements Transport.
func (r *Transport) NegotiationParams() transport.NegotiationParams {
	if err := r.waitForConnection(r.ctx); err != nil {
		r.logger.Warnf(r.ctx, "Failed to establish connection, cannot get NegotiationParams: %v", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.transport == nil {
		return transport.NegotiationParams{}
	}
	return r.transport.NegotiationParams()
}

// Read implements Transport.
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

// RxBytesCounterValue implements Transport.
func (r *Transport) RxBytesCounterValue() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.transport == nil {
		return 0
	}
	return r.transport.RxBytesCounterValue()
}

// TxBytesCounterValue implements Transport.
func (r *Transport) TxBytesCounterValue() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.transport == nil {
		return 0
	}
	return r.transport.TxBytesCounterValue()
}

// Write implements Transport.
func (r *Transport) Write(data []byte) error {
	// waitForConnection は writeReqRes の中で呼ばれる writeLoop 内で処理されるため、ここでは不要。
	// writeReqRes がエラーを返した場合、それは接続試行の失敗を含む可能性がある。
	if err := r.writeReqRes(data); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

// reconnect tries to reconnect to the server.
//
// unthreadsafe method. requires to be called with r.mu locked.
func (r *Transport) reconnect(old transport.Transport) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.statusMu.Lock()
	r.status = StatusReconnecting
	r.statusMu.Unlock()

	if old != r.transport {
		// already reconnected
		r.logger.Infof(r.ctx, "Already reconnected")
		return nil
	}

	if r.closed() {
		return errors.ErrConnectionClosed
	}
	if err := old.Close(); err != nil {
		r.logger.Infof(r.ctx, "Failed to close transport: %v", err)
	}
	var rerr error
	for i := range r.maxReconnectAttempts {
		r.logger.Infof(r.ctx, "Attempting to reconnect (%d)...", i+1)
		if r.closed() {
			return errors.ErrConnectionClosed
		}
		newTransport, err := r.reconnector.Connect()
		if err != nil {
			rerr = err
			time.Sleep(r.reconnectInterval)
			continue
		}
		r.logger.Infof(r.ctx, "Successfully reconnected on attempt %d", i+1)
		r.transport = newTransport
		r.statusMu.Lock()
		r.status = StatusConnected
		r.statusMu.Unlock()
		return nil
	}
	return fmt.Errorf("reconnect: %w", rerr)
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
