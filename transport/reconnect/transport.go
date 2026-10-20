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
// 下層 transport.Write 自体が返したエラーは、それが errors.ErrConnectionClosed と
// 判定できる場合（下層 transport が閉塞由来と確認した場合）に限りこのセンチネルで
// 包む。それ以外（真の部分送信の可能性があるエラー）はこのセンチネルで包まない。
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

	// writeMu serializes calls to the underlying transport.Write.
	// This satisfies the concurrent write restriction of the underlying implementation (e.g. gorilla/websocket).
	writeMu sync.Mutex

	// reconnectMu serializes calls to reconnect().
	// Synchronous calls from processReads wait on Lock, while triggerReconnect
	// (started by a failed Write) bails out early with TryLock to avoid duplicate runs.
	reconnectMu sync.Mutex

	// useV4Protocol enables v4 protocol features (message type byte prefix, heartbeat).
	// It is true when TransportType is set. v3 connections neither add the type byte nor send heartbeats.
	useV4Protocol bool

	// Time of the last data write (used by the heartbeat loop)
	lastWriteTime atomic.Int64

	// Transport metrics provider (RTT, CWND, etc.)
	// The provider manages its own lifecycle through Stop().
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
	// 接続が回復しない限り再接続ループは終了しません（意図された仕様です）。
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
	// MaxReconnectAttempts < 0 means unlimited

	// Get the negotiation parameters and, if available, use the heartbeat interval/timeout from them
	negParams := c.DialConfig.NegotiationParams()

	// Set the heartbeat interval. Priority: negotiation parameters, then config, then default
	heartbeatInterval := c.HeartbeatInterval
	if negParams.HeartbeatInterval != nil && *negParams.HeartbeatInterval > 0 {
		heartbeatInterval = time.Duration(*negParams.HeartbeatInterval) * time.Second
	} else if heartbeatInterval == 0 {
		heartbeatInterval = 10 * time.Second
	}

	// Set the heartbeat timeout. Priority: negotiation parameters, then config, then default
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

	// If not set yet, store the heartbeat interval and timeout in the negotiation parameters
	if negParams.HeartbeatInterval == nil {
		intervalSec := int(heartbeatInterval.Seconds())
		negParams.HeartbeatInterval = &intervalSec
	}
	if negParams.HeartbeatTimeout == nil {
		timeoutSec := int(heartbeatTimeout.Seconds())
		negParams.HeartbeatTimeout = &timeoutSec
	}

	// Store the reconnect parameters in the negotiation parameters
	if negParams.MaxReconnectAttempts == nil {
		negParams.MaxReconnectAttempts = &c.MaxReconnectAttempts
	}
	if negParams.ReconnectInterval == nil {
		reconnectIntervalSec := int(c.ReconnectInterval.Seconds())
		negParams.ReconnectInterval = &reconnectIntervalSec
	}

	// Reflect the reconnect parameters in DialConfig (so that the child dialer includes them in negotiation)
	c.DialConfig.MaxReconnectAttempts = negParams.MaxReconnectAttempts
	c.DialConfig.ReconnectInterval = negParams.ReconnectInterval
	c.DialConfig.HeartbeatInterval = negParams.HeartbeatInterval
	c.DialConfig.HeartbeatTimeout = negParams.HeartbeatTimeout

	// Create the Transport instance first; the actual connection runs in the background

	// v4 protocol features are enabled only when TransportType is set
	useV4 := c.DialConfig.TransportType != ""

	t := &Transport{
		reconnector:          nil, // set below once t.ctx is determined
		transport:            nil, // the inner transport is nil initially
		mu:                   sync.RWMutex{},
		useV4Protocol:        useV4,
		maxReconnectAttempts: c.MaxReconnectAttempts,
		reconnectInterval:    c.ReconnectInterval,
		heartbeatInterval:    heartbeatInterval,
		heartbeatTimeout:     heartbeatTimeout,
		readResCh:            make(chan *readRes, 1024),
		metricsProvider:      metrics.NewNopMetricsProvider(), // initialized with noop
		ctx:                  nil,
		cancel:               nil,
		logger:               c.Logger,
		statusMu:             sync.RWMutex{},
		status:               StatusConnecting, // the new "connecting" status
		onStatusChange:       c.OnStatusChange,
		initialConnectDoneCh: make(chan error, 1),
		negotiationParams:    negParams,
	}
	t.ctx, t.cancel = context.WithCancel(context.Background())
	// Reconnect dials use the Transport's own lifecycle ctx (t.ctx).
	// Because cancel() in CloseWithStatus can interrupt an in-progress dial, the time
	// Close blocks until the dial completes is bounded (when the dialer implements
	// ContextDialer; for a legacy Dialer the upper bound is the dialer's internal timeout).
	// t.ctx is used instead of the caller's ctx because reconnects repeat over the
	// entire lifetime of the Transport.
	t.reconnector = TransportConnectorFunc(func() (transport.Transport, error) {
		return transport.DialWithContext(t.ctx, c.Dialer, c.DialConfig)
	})

	// Run the connection process in the background
	go t.initialConnect(c.Dialer, c.DialConfig)

	if t.useV4Protocol {
		go t.heartbeatLoop()
	}
	go t.readLoop()
	return t, nil
}

// initialConnect runs the initial connection attempts in the background.
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
			// All attempts failed
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
		// Dial with r.ctx (same reason as the reconnector; cancel() in CloseWithStatus
		// also interrupts an in-progress initial dial).
		currentTr, currentErr := transport.DialWithContext(r.ctx, dialer, dialConfig)
		err = currentErr
		if currentErr == nil {
			r.mu.Lock()
			// Re-check closed while holding r.mu. CloseWithStatus runs cancel() and then
			// r.mu.Lock(), so this r.mu is the synchronization point:
			//   - If closed() == false here, cancel has not returned yet (r.ctx.Done()
			//     is not closed), so the r.mu.Lock() in CloseWithStatus, which comes later
			//     in program order, runs after this assignment completes and always
			//     closes the non-nil r.transport.
			//   - If closed() == true, CloseWithStatus may already have seen
			//     r.transport == nil and finished with "nothing to close", so we close
			//     currentTr ourselves here and discard it.
			// Without this re-check, if Close completes during dialer.Dial, nobody closes
			// currentTr, and the underlying connection and readLoop (including the internal
			// reader goroutine) leak permanently.
			// Note that this exclusion no longer holds if CloseWithStatus stops running
			// in the order "cancel first, mu.Lock second".
			//
			// Note also that what is guaranteed is only that it is "eventually closed";
			// it is not yet closed when Close() returns. CloseWithStatus does not wait for
			// initialConnect to finish (waitForReconnectToFinish waits on reconnectMu,
			// which initialConnect does not take), so the underlying connection is closed
			// only after dialer.Dial returns. With a real dialer and a long connect
			// timeout, the fd stays open in the meantime.
			if r.closed() {
				r.mu.Unlock()
				r.logger.Infof(r.ctx, "Initial connection canceled after dial; closing dialed transport.")
				if closeErr := currentTr.Close(); closeErr != nil {
					r.logger.Warnf(r.ctx, "Failed to close dialed transport after cancel: %v", closeErr)
				}
				doneProcess(errors.ErrConnectionClosed, StatusDisconnected)
				return
			}
			r.transport = currentTr
			// Initialize the metrics provider from the new transport
			if ms, ok := currentTr.(transport.MetricsSupporter); ok {
				r.metricsProvider = ms.MetricsProvider()
			} else {
				r.metricsProvider = metrics.NewNopMetricsProvider()
			}
			r.mu.Unlock()
			doneProcess(nil, StatusConnected) // update the status on successful connection
			r.logger.Infof(r.ctx, "Successfully connected.")
			return
		}
		r.logger.Warnf(r.ctx, "Initial connection attempt failed: %v", currentErr)
		r.waitReconnectInterval()
	}
}

// waitForConnection waits until the initial connection completes or the context is canceled.
// It returns nil if the connection succeeds and an error if it fails.
func (r *Transport) waitForConnection(ctx context.Context) error {
	currentStatus := r.Status()
	if currentStatus == StatusConnected {
		return nil
	}
	if currentStatus == StatusDisconnected && !r.closed() { // closed() checks ctx.Done(), so only the status is used here
		return errors.New("transport is disconnected")
	}

	if currentStatus == StatusConnecting {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.ctx.Done(): // also watch the Transport's own context
			return errors.ErrConnectionClosed
		case err, ok := <-r.initialConnectDoneCh:
			if !ok {
				// The channel is already closed (initialConnect has finished)
				// Trust the status at this point
				if r.Status() == StatusConnected {
					return nil
				}
				return errors.New("initial connection previously failed or channel closed unexpectedly")
			}
			// received a value from initialConnectDoneCh (initialConnect has just finished)
			if err != nil {
				// initialConnect finished with an error
				return fmt.Errorf("initial connection attempt failed: %w", err)
			}
			// initialConnect finished successfully (err == nil)
			if r.Status() != StatusConnected {
				// the notification reported success but the status does not match (a race is unlikely, but just in case)
				return errors.New("connection status inconsistent after initial connect success notification")
			}
			return nil
		}
	}
	// For StatusReconnecting, do not wait here; leave it to the reconnect process in each operation
	// For StatusDisconnected, it was already handled above, or it is the result of initialConnect finishing with a failure
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
			// Skip the heartbeat when not Connected, e.g. while reconnecting
			if r.Status() != StatusConnected {
				continue
			}
			// Check lastWriteTime and skip if data was sent within the heartbeat interval
			lastWrite := time.Unix(0, r.lastWriteTime.Load())
			if time.Since(lastWrite) < r.heartbeatInterval {
				continue
			}

			heartbeat, _ := (&HeartbeatMessage{}).MarshalBinary()
			if err := r.writeRaw(heartbeat); err != nil {
				// a reconnect has already been triggered, so treat the heartbeat failure as non-fatal and continue
				r.logger.Warnf(r.ctx, "Failed to send heartbeat: %v", err)
				continue
			}
			r.lastWriteTime.Store(time.Now().UnixNano())
			r.logger.Debugf(r.ctx, "Sent heartbeat")
		}
	}
}

// writeRaw synchronously calls the underlying transport.Write without adding the type byte.
// On a write error, it triggers an asynchronous reconnect only once.
func (r *Transport) writeRaw(data []byte) error {
	// Wait for the state outside writeMu. writeMu is dedicated to serializing the underlying tr.Write.
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
		// After waitForWritable() confirms Connected and before tr.Write is actually
		// called, the read loop may run doReconnect() -> old.Close(), which makes the
		// Write to the grabbed transport fail with a real network error (TOCTOU).
		// Wrap it as ErrNotConnected, making it a fallback target of multi.Transport,
		// only when the underlying transport (websocket/quic/webtransport) can
		// determine it as errors.ErrConnectionClosed. Any other error (which may be
		// a real partial send) is returned as is and stays out of the fallback.
		if errors.Is(err, errors.ErrConnectionClosed) {
			return fmt.Errorf("%w: %w", ErrNotConnected, err)
		}
		return err
	}
	return nil
}

// waitForWritable waits until the transport becomes writable.
// All returned errors are wrapped in ErrNotConnected, telling the caller that
// it failed before calling the underlying tr.Write (a fallback target of multi.Transport).
//
//   - StatusConnecting: wait for the initial connection to complete (Write compatibility right after Dial).
//   - StatusReconnecting: with finite retries, wait for completion. With unlimited
//     retries, return an error immediately (so as not to block the fallback of multi.Transport).
//   - StatusDisconnected: return an error immediately.
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

// waitForReconnectToFinish waits for the in-progress reconnect goroutine to finish.
// It only acquires and releases reconnectMu, but once the lock is acquired, any
// in-flight doReconnect is guaranteed to have returned. It is called from Close.
func (r *Transport) waitForReconnectToFinish() {
	r.reconnectMu.Lock()
	//nolint:staticcheck // SA2001: Lock/Unlock pair to wait for the in-progress reconnect to finish
	r.reconnectMu.Unlock()
}

// triggerReconnect starts reconnect() asynchronously.
// If a reconnect is already running, it bails out early with TryLock to avoid a duplicate run.
func (r *Transport) triggerReconnect(old transport.Transport) {
	if !r.reconnectMu.TryLock() {
		return
	}
	defer r.reconnectMu.Unlock()
	if err := r.doReconnect(old); err != nil {
		r.logger.Errorf(r.ctx, "Triggered reconnect failed: %v", err)
	}
}

// readResult is a read result passed from the persistent reader goroutine to readLoop.
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

		// Start a persistent reader goroutine for the current transport
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

		// Process the read results. If a reconnect is needed, needReconnect is true
		needReconnect, readErr := r.processReads(readCh, tr)
		// Wait for the reader goroutine to exit while draining readCh.
		// "It always exits because tr is closed" does not hold on its own:
		// once processReads returns through the timerC (heartbeat timeout) path,
		// nobody receives from readCh, so if the reader has produced two results
		// (the first fills the buffer), it blocks sending the second one to readCh.
		// tr.Close() does not unblock this send (it is not in Read), and r.ctx is
		// still alive, so without draining, readLoop would stop here forever.
		// The drained results are discarded (they are reads of the old generation, and
		// resending after a reconnect is handled by Reliable in the iSCP layer. The
		// needReconnect == false path is shutting down, so discarding is fine).
	drain:
		for {
			select {
			case <-readerDone:
				break drain
			case <-readCh:
			}
		}

		if !needReconnect {
			if readErr != nil {
				ch.WriteOrDone(r.ctx, &readRes{err: readErr}, r.readResCh)
			}
			return
		}
		// needReconnect == true: go to the next iteration with the new transport
		continue
	}
}

// processReads processes the read results from the persistent reader goroutine.
// It returns (true, nil) if a reconnect is needed.
// It returns (false, err) on a fatal error or normal termination.
func (r *Transport) processReads(readCh <-chan readResult, tr transport.Transport) (needReconnect bool, fatalErr error) {
	// v4 uses the heartbeat timeout; v3 has no timeout
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
			// Heartbeat timeout occurred
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
				// On a normal close, exit without reconnecting
				if transport.IsNormalClose(result.err) {
					r.logger.Infof(r.ctx, "Read loop: normal close detected, exiting without reconnect")
					r.cancel() // propagate NormalClose to writeLoop/heartbeatLoop and suppress reconnect
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

			// Reset the heartbeat timer (on every data receive)
			if heartbeatTimer != nil {
				if !heartbeatTimer.Stop() {
					select {
					case <-heartbeatTimer.C:
					default:
					}
				}
				heartbeatTimer.Reset(r.heartbeatTimeout)
			}

			// v3: pass the data as is to the upper layer
			if !r.useV4Protocol {
				ch.WriteOrDone(r.ctx, &readRes{bs: result.data, err: nil}, r.readResCh)
				continue
			}

			// v4: parse the message type byte
			msgType, parseErr := ParseMessageType(result.data)
			if parseErr != nil {
				// Protocol error: log it and trigger a reconnect
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
				// iSCP message: strip the leading type byte and pass it to the upper layer
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
// dial は r.ctx で行われるため、冒頭の cancel() が進行中の dial を中断し、
// この待機は有界になる（dialer が ContextDialer を実装している場合。従来型
// Dialer では dialer 内部のタイムアウトまでブロックする可能性が残る）。
func (r *Transport) CloseWithStatus(status transport.CloseStatus) error {
	r.cancel()

	// Wait until the in-progress reconnect observes the ctx cancellation and exits.
	// Once reconnectMu is held, every reconnect goroutine has left doReconnect.
	r.waitForReconnectToFinish()

	r.mu.Lock()
	defer r.mu.Unlock()

	// reset to noop (the underlying Transport.Close() handles Stop() of the metrics)
	r.metricsProvider = metrics.NewNopMetricsProvider()

	var err error
	// While the transport is connecting, r.transport is nil, so check for nil first
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
		return "" // or an appropriate default name
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
		// v4: prepend the 0x00 type byte
		payload = make([]byte, len(data)+1)
		payload[0] = byte(MessageTypeISCP)
		copy(payload[1:], data)
	} else {
		// v3: send as is
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

// reconnect tries to reconnect to the server.
//
// Calls are serialized by reconnectMu. Synchronous calls from processReads and the like
// wait on Lock, while triggerReconnect (started asynchronously by a failed Write)
// bails out early with TryLock.
func (r *Transport) reconnect(old transport.Transport) error {
	r.reconnectMu.Lock()
	defer r.reconnectMu.Unlock()
	return r.doReconnect(old)
}

// doReconnect is called by a caller that already holds reconnectMu.
// It does not hold r.mu during Connect() retries, so that writeRaw and readLoop can
// take a snapshot of the underlying transport (if they grab a tr in the old Closed state,
// Write/Read return an error immediately, so the caller's fallback works).
func (r *Transport) doReconnect(old transport.Transport) error {
	// No-op if a preceding reconnect has already completed
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
			// Transition to Disconnected when giving up on reconnecting.
			// This lets Write return an error immediately instead of waitForWritable polling forever.
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
			r.waitReconnectInterval()
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

// waitReconnectInterval waits for reconnectInterval until the next connection attempt.
// Unlike time.Sleep, it is cut short immediately when r.ctx is canceled (the shutdown
// is handled by the closed() check at the top of the caller's loop).
// With a wait that cannot be interrupted, doReconnect waits while holding reconnectMu,
// so CloseWithStatus (cancel -> waitForReconnectToFinish) would block for up to an extra
// reconnectInterval, and in initialConnect the goroutine would remain after Close.
func (r *Transport) waitReconnectInterval() {
	t := time.NewTimer(r.reconnectInterval)
	defer t.Stop()
	select {
	case <-t.C:
	case <-r.ctx.Done():
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

// setStatus changes the status and notifies the callback if one is set.
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

// currentMetricsProvider returns the current metrics provider under the r.mu protection.
// A lock is needed for reads as well, because reconnect() may replace r.metricsProvider.
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
