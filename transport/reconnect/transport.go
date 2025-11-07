package reconnect

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/aptpod/iscp-go/errors"
	"github.com/aptpod/iscp-go/log"
	"github.com/aptpod/iscp-go/transport"
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
	readTimeout          time.Duration

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
	negotiationParams    transport.NegotiationParams
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
	ReadTimeout          time.Duration
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
	// MaxReconnectAttempts < 0 is unlimited
	if c.ReadTimeout == 0 {
		c.ReadTimeout = 15 * time.Second
	}
	if c.Logger == nil {
		c.Logger = log.NewNop()
	}
	if c.DialConfig.TransportID == "" {
		c.DialConfig.TransportID = transport.TransportID(uuid.New().String())
	}

	// Create the Transport instance first, and perform the actual connection in the background

	t := &Transport{
		reconnector: TransportConnectorFunc(func() (transport.Transport, error) {
			cc := c.DialConfig
			cc.Reconnect = true
			return c.Dialer.Dial(cc)
		}),
		transport:            nil, // Internal transport is nil in the initial state
		mu:                   sync.RWMutex{},
		maxReconnectAttempts: c.MaxReconnectAttempts,
		reconnectInterval:    c.ReconnectInterval,
		readTimeout:          c.ReadTimeout,
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
		status:               StatusConnecting, // New "connecting" status
		initialConnectDoneCh: make(chan error, 1),
		negotiationParams:    c.DialConfig.NegotiationParams(),
	}
	t.ctx, t.cancel = context.WithCancel(context.Background())

	// Execute connection process in the background
	go t.initialConnect(c.Dialer, c.DialConfig)

	go t.pingLoop()
	go t.readLoop()
	go t.writeLoop()
	return t, nil
}

// initialConnect performs initial connection attempts in the background.
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
			// All attempts have failed
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
		currentTr, currentErr := dialer.Dial(dialConfig) // Temporary error variable in the loop
		err = currentErr                                 // Update the final error
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
			doneProcess(nil, StatusConnected) // Update status on successful connection
			r.logger.Infof(r.ctx, "Successfully connected.")
			return
		}
		r.logger.Warnf(r.ctx, "Initial connection attempt failed: %v", currentErr)
		time.Sleep(r.reconnectInterval)
	}
}

// waitForConnection waits until the initial connection is complete or the context is canceled.
// It returns nil if the connection succeeds, or an error if it fails.
func (r *Transport) waitForConnection(ctx context.Context) error {
	currentStatus := r.Status()
	if currentStatus == StatusConnected {
		return nil
	}
	if currentStatus == StatusDisconnected && !r.closed() { // closed() checks ctx.Done(), so only judge by status here
		return errors.New("transport is disconnected")
	}

	if currentStatus == StatusConnecting {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.ctx.Done(): // Also monitor the Transport's own context
			return errors.ErrConnectionClosed
		case err, ok := <-r.initialConnectDoneCh:
			if !ok {
				// Channel is already closed (initialConnect has completed)
				// Trust the status at this point
				if r.Status() == StatusConnected {
					return nil
				}
				return errors.New("initial connection previously failed or channel closed unexpectedly")
			}
			// Received value from initialConnectDoneCh (initialConnect just completed)
			if err != nil {
				// initialConnect completed with error
				return fmt.Errorf("initial connection attempt failed: %w", err)
			}
			// initialConnect completed successfully (err == nil)
			if r.Status() != StatusConnected {
				// Notification is successful but status doesn't match for some reason (race condition unlikely but just in case)
				return errors.New("connection status inconsistent after initial connect success notification")
			}
			return nil
		}
	}
	// For StatusReconnecting, don't wait here, leave it to the reconnection process in each operation
	// For StatusDisconnected, it was already handled above or is the result of initialConnect completing and failing
	return nil
}

func (r *Transport) nextID() int64 {
	return r.writeID.Add(1)
}

func (r *Transport) pingLoop() {
	r.logger.Infof(r.ctx, "Starting ping loop")
	// Wait until the internal transport is established
	if err := r.waitForConnection(r.ctx); err != nil {
		r.logger.Errorf(r.ctx, "Ping loop canceled, failed to establish connection: %v", err)
		return
	}
	// r.closed() check is done inside waitForConnection and in the loop select, so not needed here

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
				// If the internal transport is not yet established, wait for the connection
				if err := r.waitForConnection(r.ctx); err != nil {
					writeOrDone(r.ctx, writeRes{err: fmt.Errorf("failed to establish initial connection: %w", err)}, r.writeResCh[data.id])
					continue
				}
				// Check trEstablished again after waitForConnection
				r.mu.RLock()
				trEstablished = r.transport != nil
				r.mu.RUnlock()
				if !trEstablished { // If still not established, error
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

			// Read with timeout using goroutine
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
				// Timeout occurred - trigger reconnection
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
				switch {
				case IsPing(data):
					writeOrDone(r.ctx, Ping{}, r.pingCh)
					continue
				}
				writeOrDone(r.ctx, &readRes{bs: data, err: nil}, r.readResCh)
			}
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
	// if transport is connecting, when r.transport is nil, so check nil first
	if r.transport != nil {
		if c, ok := r.transport.(transport.Closer); ok {
			err = c.CloseWithStatus(status)
		}
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
		return "" // Or an appropriate default name
	}
	return r.transport.Name()
}

// NegotiationParams implements Transport.
func (r *Transport) NegotiationParams() transport.NegotiationParams {
	return r.negotiationParams
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
	// waitForConnection is handled inside writeLoop which is called within writeReqRes, so not needed here.
	// If writeReqRes returns an error, it may include a connection attempt failure.
	if err := r.writeReqRes(data); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

// reconnect tries to reconnect to the server.
//
// unthreadsafe method. requires to be called with r.mu locked.
func (r *Transport) reconnect(old transport.Transport) error {
	r.logger.Infof(r.ctx, "Reconnect called, acquiring lock...")
	r.mu.Lock()
	defer r.mu.Unlock()

	r.logger.Infof(r.ctx, "Lock acquired, changing status to StatusReconnecting")
	r.statusMu.Lock()
	r.status = StatusReconnecting
	r.statusMu.Unlock()

	if old != r.transport {
		// already reconnected
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

	var rerr error
	for i := 0; ; i++ {
		if r.maxReconnectAttempts >= 0 && i >= r.maxReconnectAttempts {
			// All attempts have failed
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
