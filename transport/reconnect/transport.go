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
	mu                   sync.Mutex
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

	var tr transport.Transport
	var err error
	for range c.MaxReconnectAttempts {
		tr, err = c.Dialer.Dial(c.DialConfig)
		if err == nil {
			break
		}
		time.Sleep(c.ReconnectInterval)
		continue
	}
	if _, ok := tr.(transport.Closer); !ok {
		return nil, fmt.Errorf("transport does not implement Closer")
	}

	if err != nil {
		return nil, fmt.Errorf("reconnectable dial: %w", err)
	}
	t := &Transport{
		reconnector: TransportConnectorFunc(func() (transport.Transport, error) {
			cc := c.DialConfig
			cc.Reconnect = true
			return c.Dialer.Dial(cc)
		}),
		transport:            tr,
		mu:                   sync.Mutex{},
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
	}
	t.ctx, t.cancel = context.WithCancel(context.Background())
	go t.pingLoop()
	go t.readLoop()
	go t.writeLoop()
	return t, nil
}

func (r *Transport) nextID() int64 {
	return r.writeID.Add(1)
}

func (r *Transport) pingLoop() {
	r.logger.Infof(r.ctx, "Starting ping loop")
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
			for {
				r.mu.Lock()
				tr := r.transport
				r.mu.Unlock()
				err := tr.Write(data.bs)
				if err != nil {
					if r.closed() {
						return
					}
					r.logger.Infof(r.ctx, "Reconnecting in write loop due to error: %v", err)
					r.mu.Lock()
					if reconnectErr := r.reconnect(tr); reconnectErr != nil {
						r.mu.Unlock()
						writeOrDone(r.ctx, writeRes{err: fmt.Errorf("reconnect cause[%v]: %w", err, reconnectErr)}, r.writeResCh[data.id])
						return
					}
					r.mu.Unlock()
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
					return
				}

				r.logger.Infof(r.ctx, "Reconnecting in read loop due to error: %v", err)
				r.mu.Lock()
				if reconnectErr := r.reconnect(tr); reconnectErr != nil {
					r.mu.Unlock()
					writeOrDone(r.ctx, &readRes{err: fmt.Errorf("reconnect cause[%v]: %w", err, reconnectErr)}, r.readResCh)
					return
				}
				r.mu.Unlock()
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
	r.mu.Lock()
	defer r.mu.Unlock()
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
	return err
}

// Name implements Transport.
func (r *Transport) Name() transport.Name {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.transport.Name()
}

// NegotiationParams implements Transport.
func (r *Transport) NegotiationParams() transport.NegotiationParams {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.transport.NegotiationParams()
}

// Read implements Transport.
func (r *Transport) Read() ([]byte, error) {
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
	return r.transport.RxBytesCounterValue()
}

// TxBytesCounterValue implements Transport.
func (r *Transport) TxBytesCounterValue() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.transport.TxBytesCounterValue()
}

// Write implements Transport.
func (r *Transport) Write(data []byte) error {
	if err := r.writeReqRes(data); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

// reconnect tries to reconnect to the server.
//
// unthreadsafe method. requires to be called with r.mu locked.
func (r *Transport) reconnect(old transport.Transport) error {
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
		if err == nil {
			if _, err := newTransport.Read(); err != nil {
				rerr = err
				time.Sleep(r.reconnectInterval)
				continue
			}
			r.logger.Infof(r.ctx, "Successfully reconnected on attempt %d", i+1)
			r.transport = newTransport
			return nil
		}
		rerr = err
		time.Sleep(r.reconnectInterval)
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
