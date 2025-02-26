package multi

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/aptpod/iscp-go/errors"
	"github.com/aptpod/iscp-go/internal/ch"
	"github.com/aptpod/iscp-go/log"
	"github.com/aptpod/iscp-go/transport"
)

// ErrInvalidSchedulerMode represents an error when an invalid scheduler mode is specified
var ErrInvalidSchedulerMode = errors.New("invalid scheduler mode")

// ErrMissingEventScheduler represents an error when event scheduler configuration is missing
var ErrMissingEventScheduler = errors.New("required EventScheduler and Subscriber")

var _ transport.Transport = (*Transport)(nil)

type readRes struct {
	bs  []byte
	err error
}

type writeReq struct {
	id int64
	bs []byte
}

type writeRes struct {
	id  int64
	err error
}

type Transport struct {
	// Context management
	ctx    context.Context
	cancel context.CancelFunc

	// Channel management
	readResCh  chan *readRes
	writeReqCh chan *writeReq
	writeResCh map[int64]chan *writeRes

	// Synchronization
	mu sync.RWMutex

	// Transport management
	transportMap          map[transport.TransportID]transport.Transport
	lastReadTransportID   transport.TransportID
	lastReadTransportIDmu sync.RWMutex
	transportIDCh         <-chan transport.TransportID
	currentTransportID    transport.TransportID

	// Logging
	logger log.Logger
}

type SchedulerMode int

const (
	SchedulerModePolling SchedulerMode = iota
	SchedulerModeEvent
)

type TransportMap map[transport.TransportID]transport.Transport

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
}

func NewTransport(c TransportConfig) (*Transport, error) {
	if err := validateConfig(&c); err != nil {
		return nil, err
	}

	m := &Transport{
		readResCh:          make(chan *readRes, 1024),
		writeReqCh:         make(chan *writeReq, 1024),
		writeResCh:         make(map[int64]chan *writeRes),
		transportMap:       c.TransportMap,
		currentTransportID: c.InitialTransportID,
		logger:             c.Logger,
	}

	m.ctx, m.cancel = context.WithCancel(context.Background())

	if err := m.initializeScheduler(c); err != nil {
		return nil, fmt.Errorf("initialize scheduler: %w", err)
	}

	go m.transportIDLoop()
	go m.readLoop()
	go m.writeLoop()

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
		if t.NegotiationParams().TransportGroupTotalCount != len(c.TransportMap) {
			return errors.New("transport group total count must be equal to the number of transports")
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
		if m.currentTransportID != id {
			m.logger.Infof(m.ctx, "Switching transport to %s", id)
			m.currentTransportID = id
		}

		m.mu.Unlock()
	}
}

func (m *Transport) readLoop() {
	m.logger.Infof(m.ctx, "Starting read loop")
	defer m.logger.Infof(m.ctx, "Stopping read loop")
	var wg sync.WaitGroup
	for tID, t := range m.transportMap {
		wg.Add(1)
		go func(tID transport.TransportID, t transport.Transport) {
			defer wg.Done()
			for {
				select {
				case <-m.ctx.Done():
					return
				default:
				}
				res, err := t.Read()
				m.lastReadTransportIDmu.Lock()
				m.lastReadTransportID = tID
				m.lastReadTransportIDmu.Unlock()
				ch.WriteOrDone(m.ctx, &readRes{bs: res, err: err}, m.readResCh)
			}
		}(tID, t)
	}
	<-m.ctx.Done()
	wg.Wait()
}

func (m *Transport) writeLoop() {
	m.logger.Infof(m.ctx, "Starting write loop")
	defer m.logger.Infof(m.ctx, "Stopping write loop")

	for data := range ch.ReadOrDone(m.ctx, m.writeReqCh) {
		m.handleWrite(data)
	}
}

func (m *Transport) handleWrite(data *writeReq) {
	m.mu.RLock()
	tID := m.currentTransportID
	m.mu.RUnlock()

	err := m.transportMap[tID].Write(data.bs)

	m.mu.Lock()
	defer m.mu.Unlock()

	m.writeResCh[data.id] <- &writeRes{
		id:  data.id,
		err: err,
	}
}

// AsUnreliable implements Transport.
func (m *Transport) AsUnreliable() (tr transport.UnreliableTransport, ok bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.transportMap[m.currentTransportID].AsUnreliable()
}

// Close implements Transport.
func (m *Transport) Close() error {
	m.cancel()
	var errs error
	for _, v := range m.transportMap {
		errs = errors.Join(errs, v.Close())
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
	return m.transportMap[m.currentTransportID].NegotiationParams()
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
	return m.transportMap[m.currentTransportID].Write(bs)
}
