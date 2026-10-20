package iscp

import (
	"context"
	"sync"
	"time"

	"github.com/aptpod/iscp-go/v2/errors"

	"github.com/aptpod/iscp-go/v2/log"
	"github.com/aptpod/iscp-go/v2/message"
	"github.com/aptpod/iscp-go/v2/transport"
)

var (
	// ストリームが存在しません。
	ErrStreamNotFound = errors.New("stream not found")
)

// Token は 認証トークンです。
type Token string

// TokenSource は認証トークン取得するためのインターフェースです。
type TokenSource interface {
	// Tokenはトークンを取得します。
	//
	// iSCPコネクションを開くたびに（再接続時を含む）、このメソッドをコールします。
	// このメソッドから毎回新しいトークンを返却することで、トークンの有効期限切れを回避することができます。
	Token() (Token, error)
}

// TokenSourceFunc は認証トークン取得するための関数です。
//
// TokenSourceFuncは、TokenSourceとして使用できます。TokenSourceとして使用した場合、関数をそのままコールします。
type TokenSourceFunc func() (Token, error)

// Tokenはトークンを取得します。
func (f TokenSourceFunc) Token() (Token, error) {
	return f()
}

// StaticTokenSource は静的に認証トークンを指定するTokenSourceです。
type StaticTokenSource struct {
	token string
}

// NewStaticTokenSource は StaticTokenSource を生成します。
func NewStaticTokenSource(static string) *StaticTokenSource {
	return &StaticTokenSource{
		token: static,
	}
}

// Tokenはトークンを取得します。
//
// 常に同じトークンを返却します。
func (n *StaticTokenSource) Token() (Token, error) {
	return Token(n.token), nil
}

// Connect、はiSCP接続を行いコネクションを返却します。
//
// addressはサーバーがリスンするホスト:ポート（例 127.0.0.1:8080）を指定します。
func Connect(address string, transport TransportName, opts ...ConnOption) (*Conn, error) {
	conf := defaultClientConfig
	for _, o := range opts {
		o(&conf)
	}
	conf.Address = address
	conf.Transport = transport

	return ConnectWithConfig(&conf)
}

// ConnectWithConfigは、指定された設定に従ってiSCP接続を行いコネクションを返却します。
//
// このメソッドは、再接続などの際に、ConnのConfigメソッドによって取得した設定を引数にして使用することを想定しています。
// 通常のiSCP接続は Connectメソッド を使用してください。
func ConnectWithConfig(c *ConnConfig) (*Conn, error) {
	if c.Encoding == "" {
		c.Encoding = EncodingNameProtobuf
	}
	if c.Logger == nil {
		c.Logger = log.NewNop()
	}

	if c.upstreamRepository == nil {
		c.upstreamRepository = newInmemStreamRepository()
	}

	if c.downstreamRepository == nil {
		c.downstreamRepository = newInmemStreamRepository()
	}

	if c.TokenSource == nil {
		c.TokenSource = TokenSourceFunc(func() (Token, error) { return Token(""), nil })
	}
	if c.ReconnectedEventHandler == nil {
		c.ReconnectedEventHandler = nopReconnectedEventHandler{}
	}
	if c.DisconnectedEventHandler == nil {
		c.DisconnectedEventHandler = nopDisconnectedEventHandler{}
	}

	// There is no public API for passing the caller's ctx to the initial
	// connection. The upper bound for the initial connection is the dialer
	// timeout (DialTimeout for websocket, 10 seconds by default) plus the
	// handshake watchdog (30 seconds). Dial can be interrupted by ctx only
	// on the reconnect path (connLifecycle.reconnect).
	wireConn, err := c.dialWire(context.Background())
	if err != nil {
		return nil, errors.Errorf("failed to connect wire: %w", err)
	}
	conn := &Conn{
		wireConn:              wireConn,
		downstreamIDGenerator: NewAliasGenerator(1),
		replyCallChs:          make(map[string]chan *message.DownstreamCall),
		downstreamCallCh:      make(chan *message.DownstreamCall, 1024),
		replyCallCh:           make(chan *message.DownstreamCall, 1024),
		upstreamCallAckCh:     make(map[string]chan *message.UpstreamCallAck),
		upstreamRepository:    c.upstreamRepository,
		downstreamRepository:  c.downstreamRepository,
		eventDispatcher:       newEventDispatcher(),

		upstreams:   make(map[*Upstream]struct{}),
		downstreams: make(map[*Downstream]struct{}),

		logger: c.Logger,
		state:  newConnState(),

		Config: *c,
	}
	conn.setE2ECallbacks(wireConn)
	// Start after setE2ECallbacks completes. readReliableLoop reads
	// onDownstreamCall/onUpstreamCallAck etc. without locks, so starting
	// before these fields are set causes a data race
	// (see the comment on newProtocolSession).
	go wireConn.runWire()

	lc := &connLifecycle{conn: conn}
	go lc.run(context.Background())

	return conn, nil
}

// Connは、iSCPのコネクションです。
type Conn struct {
	wireConnMu            sync.Mutex
	wireConn              *protocolSession
	downstreamIDGenerator *AliasGenerator

	replyCallsChsMu   sync.RWMutex
	replyCallChs      map[string]chan *message.DownstreamCall
	replyCallCh       chan *message.DownstreamCall
	downstreamCallCh  chan *message.DownstreamCall
	upstreamCallAckMu sync.RWMutex
	upstreamCallAckCh map[string]chan *message.UpstreamCallAck

	upstreamMu   sync.Mutex
	upstreams    map[*Upstream]struct{}
	downstreamMu sync.Mutex
	downstreams  map[*Downstream]struct{}

	upstreamRepository   upstreamRepository
	downstreamRepository downstreamRepository
	logger               log.Logger

	state           *connStatus
	eventDispatcher *eventDispatcher

	// コネクションの設定
	Config ConnConfig
}

func (c *Conn) isClosed() bool {
	return c.state.Is(connStatusClosed)
}

func (c *Conn) registerUpstream(up *Upstream) error {
	ctx := context.Background()
	c.upstreamMu.Lock()
	defer c.upstreamMu.Unlock()

	c.upstreams[up] = struct{}{}

	_, err := c.upstreamRepository.SaveUpstream(ctx, up.ID, *up.State())
	return err
}

func (c *Conn) unregisterUpstream(up *Upstream) {
	ctx := context.Background()
	c.upstreamMu.Lock()
	defer c.upstreamMu.Unlock()

	if _, ok := c.upstreams[up]; !ok {
		return
	}
	delete(c.upstreams, up)

	if err := c.upstreamRepository.RemoveUpstreamByID(ctx, up.ID); err != nil {
		c.logger.Warnf(ctx, "[%v] upstreamRepository remove error: %v", up.ID, err)
	}
}

func (c *Conn) registerDownstream(down *Downstream) error {
	ctx := context.Background()
	c.downstreamMu.Lock()
	defer c.downstreamMu.Unlock()

	c.downstreams[down] = struct{}{}

	_, err := c.downstreamRepository.SaveDownstream(ctx, down.ID, *down.State())
	return err
}

func (c *Conn) unregisterDownstream(down *Downstream) {
	ctx := context.Background()
	c.downstreamMu.Lock()
	defer c.downstreamMu.Unlock()

	if _, ok := c.downstreams[down]; !ok {
		return
	}
	delete(c.downstreams, down)

	if err := c.downstreamRepository.RemoveDownstreamByID(ctx, down.ID); err != nil {
		c.logger.Warnf(ctx, "[%v] downstreamRepository remove error: %v", down.ID, err)
	}
}

// snapshotWireConn gets a reference to the current wireConn within a short lock section.
//
// Calls on the returned session must be made without holding the lock
// (a round trip while holding it keeps the lock held while the server does
// not respond, and drags Close etc. along). If wireConn is replaced by a
// reconnect after the snapshot is taken, the old session is already closed,
// so calls fail with ErrConnectionClosed. The caller must retry via c.send
// or return the error.
func (c *Conn) snapshotWireConn() *protocolSession {
	c.wireConnMu.Lock()
	defer c.wireConnMu.Unlock()
	return c.wireConn
}

// OpenUpstreamは、アップストリームを開きます。
func (c *Conn) OpenUpstream(ctx context.Context, sessionID string, opts ...UpstreamOption) (*Upstream, error) {
	if c.isClosed() {
		return nil, errors.ErrConnectionClosed
	}

	upconf := defaultUpstreamConfig
	for _, opt := range opts {
		opt(&upconf)
	}
	upconf.SessionID = sessionID

	var resp *message.UpstreamOpenResponse
	var wireConn *protocolSession
	err := c.send(ctx, func(ctx context.Context) error {
		// Perform the round trip on the snapshot outside the lock. If it is
		// replaced by a reconnect during the round trip, ErrConnectionClosed is
		// returned, and c.send waits for the reconnect to complete and retries
		// with a new snapshot.
		wireConn = c.snapshotWireConn()
		r, err := wireConn.SendUpstreamOpenRequest(ctx, &message.UpstreamOpenRequest{
			SessionID:      upconf.SessionID,
			AckInterval:    *upconf.AckInterval,
			ExpiryInterval: upconf.ExpiryInterval,
			DataIDs:        upconf.DataIDs,
			QoS:            upconf.QoS,
			ExtensionFields: &message.UpstreamOpenRequestExtensionFields{
				Persist: upconf.Persist,
			},
		})
		if err != nil {
			return errors.Errorf("failed to SendUpstreamOpenRequest: %w", err)
		}
		resp = r
		return nil
	})
	if err != nil {
		return nil, err
	}
	if resp.ResultCode != message.ResultCodeSucceeded {
		return nil, errors.FailedMessageError{
			ResultCode:      resp.ResultCode,
			ResultString:    resp.ResultString,
			ReceivedMessage: resp,
		}
	}

	// From here on, consistently use the session (snapshot) that answered the
	// open round trip. This session knows the stream alias, and the subscription
	// only looks up a map in the session without I/O, and the alias was already
	// registered in the same round trip, so it always succeeds. If the session
	// was replaced, subsequent sends fail, and the resume path
	// (connLifecycle.resumeAllStreams) re-establishes it on the new session.
	ch, err := wireConn.SubscribeUpstreamChunkAck(ctx, resp.AssignedStreamIDAlias)
	if err != nil {
		return nil, errors.Errorf("failed to SubscribeUpstreamChunkAck: %w", err)
	}

	revDataIDAliases := make(map[message.DataID]uint32)
	for k, v := range resp.DataIDAliases {
		revDataIDAliases[*v] = k
	}

	ctx, cancel := context.WithCancel(context.Background())
	// Head of the send-order ticket chain. Place a closed channel so that the
	// first chunk can be sent without waiting.
	initialSendDone := make(chan struct{})
	close(initialSendDone)
	u := &Upstream{
		ctx:              ctx,
		cancel:           cancel,
		ID:               resp.AssignedStreamID,
		dataIDAliases:    resp.DataIDAliases,
		revDataIDAliases: revDataIDAliases,
		ServerTime:       resp.ServerTime,
		idAlias:          resp.AssignedStreamIDAlias,
		wireConn:         wireConn,
		sequence:         newSequenceNumberGenerator(0),
		logger:           c.logger,

		ackCh:        ch,
		dpgCh:        make(chan *DataPointGroup),
		sentBuf:      make(map[uint32]DataPointGroups),
		keepPayload:  upconf.QoS == message.QoSReliable,
		resCh:        make(chan []*message.UpstreamChunkResult, 8),
		closeTimeout: *upconf.CloseTimeout,

		afterHooker:          upconf.ReceiveAckHooker,
		sendDataPointsHooker: upconf.SendDataPointsHooker,
		eventDispatcher:      newEventDispatcher(),

		connState:               c.state,
		explicitlyFlushCh:       make(chan (<-chan struct{})),
		explicitlyFlushResultCh: make(chan error),
		Config:                  upconf,
		state:                   newStreamState(),
		sendBuffer:              map[message.DataID]DataPoints{},

		upstreamChunkResultChs: map[uint32]chan *message.UpstreamChunkResult{},
		receivedAckCh:          make(chan struct{}),
		lastSendDone:           initialSendDone,

		resumeToken: resolveResumeToken(wireConn, resp.ResumeToken),
	}
	go func() {
		defer cancel()
		c.state.WaitUntil(ctx, connStatusClosed)
	}()

	if err := c.registerUpstream(u); err != nil {
		cancel()
		return nil, err
	}

	go func() {
		defer c.unregisterUpstream(u)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			u.eventDispatcher.dispatchLoop(ctx)
		}()
		for {
			if err := u.run(); err != nil {
				if c.isClosed() || u.isClosed() {
					return
				}
				u.logger.Infof(ctx, "Wait until connected... upstreamID:[%s]", u.ID)
				if err := c.state.WaitUntilOrClosed(ctx, connStatusConnected); err != nil {
					u.logger.Errorf(ctx, "failed to wait state in resume upstream: %+v", err)
					return
				}
				// resume has already been completed by connLifecycle.resumeAllStreams().
				// The stream may be closed when resume fails.
				if u.isClosed() {
					return
				}
				continue
			}
			return
		}
	}()

	return u, nil
}

// OpenDownstreamは、ダウンストリームを開きます。
func (c *Conn) OpenDownstream(ctx context.Context, filters []*message.DownstreamFilter, opts ...DownstreamOption) (*Downstream, error) {
	if c.isClosed() {
		return nil, errors.ErrConnectionClosed
	}
	downconf := defaultDownstreamConfig
	for _, opt := range opts {
		opt(&downconf)
	}
	downconf.Filters = filters

	var (
		resp           *message.DownstreamOpenResponse
		err            error
		dpsCh          <-chan *message.DownstreamChunk
		ackCompCh      <-chan *message.DownstreamChunkAckComplete
		metaCh         <-chan *message.DownstreamMetadata
		aliasGenerator = NewAliasGenerator(0)
		aliases        = make(map[uint32]*message.DataID, len(downconf.DataIDs))
		revAliases     = make(map[message.DataID]uint32, len(downconf.DataIDs))
	)
	for _, v := range downconf.DataIDs {
		aliases[aliasGenerator.Next()] = v
		revAliases[*v] = aliasGenerator.CurrentValue()
	}
	alias := c.downstreamIDGenerator.Next()

	var wireConn *protocolSession
	err = c.send(ctx, func(ctx context.Context) error {
		// Subscribe and perform the round trip on the snapshot outside the lock.
		// On a retry by c.send, discard the subscription on the closed session and
		// start over from subscribing on the new snapshot.
		wireConn = c.snapshotWireConn()
		dpsCh, err = wireConn.SubscribeDownstreamChunk(ctx, alias, downconf.QoS)
		if err != nil {
			return errors.Errorf("failed SubscribeDownstreamChunk: %w", err)
		}
		ackCompCh, err = wireConn.SubscribeDownstreamChunkAckComplete(ctx, alias)
		if err != nil {
			return errors.Errorf("failed SubscribeDownstreamChunkAckComplete: %w", err)
		}

		metaCh, err = c.subscribeDownstreamMetadata(ctx, wireConn, alias, filters)
		if err != nil {
			return errors.Errorf("failed subscribeDownstreamMetadata: %w", err)
		}

		resp, err = wireConn.SendDownstreamOpenRequest(ctx, &message.DownstreamOpenRequest{
			DesiredStreamIDAlias: alias,
			DownstreamFilters:    filters,
			DataIDAliases:        aliases,
			QoS:                  downconf.QoS,
			ExpiryInterval:       downconf.ExpiryInterval,
			OmitEmptyChunk:       downconf.OmitEmptyChunk,
		})
		return err
	})
	if err != nil {
		return nil, errors.Errorf("failed SendDownstreamOpenRequest: %w", err)
	}

	if resp.ResultCode != message.ResultCodeSucceeded {
		return nil, &errors.FailedMessageError{
			ResultCode:      resp.ResultCode,
			ResultString:    resp.ResultString,
			ReceivedMessage: resp,
		}
	}

	if downconf.AckFlushInterval == nil {
		downconf.AckFlushInterval = &defaultAckFlushInterval
	}

	ctx, cancel := context.WithCancel(context.Background())
	down := &Downstream{
		ctx:                         ctx,
		cancel:                      cancel,
		ID:                          resp.AssignedStreamID,
		dataIDAliases:               aliases,
		revDataIDAliases:            revAliases,
		lastIssuedDataIDAlias:       aliasGenerator.CurrentValue(),
		upstreamInfos:               make(map[uint32]*message.UpstreamInfo),
		lastIssuedUpstreamInfoAlias: 0,
		lastIssuedAckSequenceNumber: 0,
		ServerTime:                  resp.ServerTime,
		wireConn:                    wireConn,
		idAlias:                     alias,
		dpsCh:                       dpsCh,
		ackCompCh:                   ackCompCh,
		metaCh:                      metaCh,
		processedDataPointsCh:       make(chan *DownstreamChunk, 1024),
		metadataCh:                  make(chan *message.DownstreamMetadata, 1024),

		dataIDAliasGenerator:       aliasGenerator,
		upstreamInfoAliasGenerator: NewAliasGenerator(0),

		ackFlushInterval:      *downconf.AckFlushInterval,
		closeTimeout:          *downconf.CloseTimeout,
		chunkAckIDSequence:    newSequenceNumberGenerator(0),
		upstreamInfoAckBuffer: make(map[uint32]*message.UpstreamInfo),
		dataIDAckBuffer:       make(map[uint32]*message.DataID),
		resultAckBuffer:       make([]*message.DownstreamChunkResult, 0),
		finalAckFlushed:       make(chan struct{}),
		eventDispatcher:       newEventDispatcher(),

		logger: c.logger,

		connStatus: c.state,
		state:      newStreamState(),
		Config:     downconf,

		resumeToken: resolveResumeToken(wireConn, resp.ResumeToken),
	}
	go func() {
		defer cancel()
		c.state.WaitUntil(ctx, connStatusClosed)
	}()

	if err := c.registerDownstream(down); err != nil {
		cancel()
		return nil, err
	}

	go func() {
		defer c.unregisterDownstream(down)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			down.eventDispatcher.dispatchLoop(ctx)
		}()
		for {
			if err := down.run(); err != nil {
				if c.isClosed() || down.isClosed() {
					return
				}
				c.logger.Infof(ctx, "Wait until connected... downstreamID:[%s]", down.ID)
				if err := c.state.WaitUntilOrClosed(ctx, connStatusConnected); err != nil {
					down.logger.Errorf(ctx, "Failed to wait state in resume downstream: %+v", err)
					return
				}
				// resume has already been completed by connLifecycle.resumeAllStreams().
				// The stream may be closed when resume fails.
				if down.isClosed() {
					return
				}
				continue
			}
			return
		}
	}()
	return down, nil
}

// SendBaseTimeは、基準時刻を送信します。
func (c *Conn) SendBaseTime(ctx context.Context, bt *message.BaseTime, opts ...SendMetadataOption) error {
	return c.SendMetadata(ctx, bt, opts...)
}

// SendMetadataは、メタデータを送信します。
func (c *Conn) SendMetadata(ctx context.Context, meta message.SendableMetadata, opts ...SendMetadataOption) error {
	opt := defaultSendMetadataOptions
	for _, v := range opts {
		v(&opt)
	}
	return c.send(ctx, func(ctx context.Context) error {
		upmeta := &message.UpstreamMetadata{
			Metadata: meta,
			ExtensionFields: &message.UpstreamMetadataExtensionFields{
				Persist: opt.Persist,
			},
		}
		// Perform the round trip on the snapshot outside the lock (on reconnect,
		// c.send retries with a new snapshot).
		resp, err := c.snapshotWireConn().SendUpstreamMetadata(ctx, upmeta)
		if err != nil {
			return err
		}
		if resp.ResultCode != message.ResultCodeSucceeded {
			return &errors.FailedMessageError{
				ResultCode:      resp.ResultCode,
				ResultString:    resp.ResultString,
				ReceivedMessage: resp,
			}
		}
		return nil
	})
}

// send is a wrapper function for sending messages that takes reconnection into account.
//
// `f` is expected to send a message to the server using the wire-layer Conn.
// If `f` returns `ErrConnectionClosed` (or an error derived from it), send waits for the reconnect to complete and then retries.
// If the Connection is explicitly closed in the meantime, it returns `ErrConnectionClosed`.
func (c *Conn) send(ctx context.Context, f func(context.Context) error) error {
	for {
		if err := c.state.WaitUntilOrClosed(ctx, connStatusConnected); err != nil {
			return err
		}
		if err := f(ctx); err != nil {
			if !errors.Is(err, errors.ErrConnectionClosed) {
				return err
			}
			if c.state.CompareAndSwapNot(connStatusClosed, connStatusReconnecting) {
				continue
			}
			return errors.ErrConnectionClosed
		}
		return nil
	}
}

func (c *Conn) saveAndClearAllUpstreams(ctx context.Context) {
	c.upstreamMu.Lock()
	defer c.upstreamMu.Unlock()
	for up := range c.upstreams {
		if _, err := c.upstreamRepository.SaveUpstream(ctx, up.ID, *up.State()); err != nil {
			c.logger.Warnf(ctx, "[%v] upstream repository save error: %v", up.ID, err)
			continue
		}
	}
	c.upstreams = make(map[*Upstream]struct{})
}

func (c *Conn) saveAndClearAllDownstreams(ctx context.Context) {
	c.downstreamMu.Lock()
	defer c.downstreamMu.Unlock()
	for down := range c.downstreams {
		if _, err := c.downstreamRepository.SaveDownstream(ctx, down.ID, *down.State()); err != nil {
			c.logger.Warnf(ctx, "[%v] downstream repository save error: %v", down.ID, err)
			continue
		}
	}
	c.downstreams = make(map[*Downstream]struct{})
}

// disconnectSendTimeout is the upper limit on how long Close waits to send the Disconnect message.
// It is a grace period so that Close does not block indefinitely even when the underlying
// transport keeps blocking (e.g. a multi.Transport whose sub-connections are all disconnected).
const disconnectSendTimeout = 3 * time.Second

func (c *Conn) close(ctx context.Context, msg *message.Disconnect) error {
	if c.state.Swap(connStatusClosed) != connStatusClosed {
		c.saveAndClearAllUpstreams(ctx)
		c.saveAndClearAllDownstreams(ctx)
	}

	// Sections where wireConnMu is held:
	//   - reconnect: only the pointer read and pointer swap (closing the old
	//     session and dialing are outside the lock) -> bounded
	//   - OpenUpstream / OpenDownstream / SendMetadata / e2e call:
	//     only taking the snapshot (the round trip is outside the lock) -> bounded
	//   - close itself: sending Disconnect is bounded by disconnectSendTimeout via select.
	//     Whether wireConn.Close() is bounded depends on the underlying transport,
	//     but reconnect.Transport interrupts an in-progress dial with its own ctx
	//     on Close, so it is bounded (only when a legacy Dialer is plugged in
	//     can it wait up to the dialer's internal timeout)
	// Holds other than close itself are bounded, so a plain Lock() wait here is
	// also bounded, and acquiring with a timeout (the former TryLock + polling) is
	// unnecessary. Keeping TryLock would leave a path that gives up acquiring and
	// closes only the old wireConn before returning, which causes a leak window
	// where nobody closes the new session established by reconnect (see the
	// comment inside reconnect in conn_lifecycle.go).
	c.wireConnMu.Lock()
	defer c.wireConnMu.Unlock()

	sendErrCh := make(chan error, 1)
	go func() { sendErrCh <- c.wireConn.SendDisconnect(ctx, msg) }()

	var err error
	select {
	case err = <-sendErrCh:
	case <-time.After(disconnectSendTimeout):
		c.logger.Warnf(ctx, "Timed out sending Disconnect after %v. Closing transport anyway.", disconnectSendTimeout)
		return c.wireConn.Close()
	case <-ctx.Done():
		c.logger.Warnf(ctx, "Context done while sending Disconnect: %v. Closing transport anyway.", ctx.Err())
		return c.wireConn.Close()
	}

	if err != nil {
		if closeErr := c.wireConn.Close(); closeErr != nil {
			c.logger.Warnf(ctx, "Failed to send Disconnect: %w", err)
			return closeErr
		}
		if errors.Is(err, errors.ErrConnectionClosed) {
			return nil
		}
		return err
	}
	return c.wireConn.Close()
}

// Closeは、コネクションを閉じます。
func (c *Conn) Close(ctx context.Context) error {
	return c.close(ctx, &message.Disconnect{
		ResultCode:   message.ResultCodeNormalClosure,
		ResultString: "NormalClosure",
	})
}

// UnderlyingTransport は内部で使用しているトランスポートを返します。
func (c *Conn) UnderlyingTransport() transport.ReadWriter {
	return c.snapshotWireConn().UnderlyingTransport()
}

// setE2ECallbacks sets the E2E call callbacks on protocolSession.
// Because readReliableLoop routes messages directly on receipt,
// no intermediate channel or dedicated goroutine is needed.
func (c *Conn) setE2ECallbacks(session *protocolSession) {
	session.onDownstreamCall = func(dc *message.DownstreamCall) {
		// request call
		if dc.RequestCallID == "" {
			select {
			case c.downstreamCallCh <- dc:
			default:
				c.logger.Warnf(context.Background(), "Discarded a e2e downstream call %+v", dc)
			}
			return
		}
		// reply call
		select {
		case c.replyCallCh <- dc:
		default:
			c.logger.Warnf(context.Background(), "Discarded a e2e reply call %+v", dc)
		}
		c.replyCallsChsMu.Lock()
		ch, ok := c.replyCallChs[dc.RequestCallID]
		if !ok {
			c.replyCallsChsMu.Unlock()
			c.logger.Warnf(context.Background(), "No reply for request call id: %v", dc.RequestCallID)
			return
		}
		delete(c.replyCallChs, dc.RequestCallID)
		c.replyCallsChsMu.Unlock()

		ch <- dc // non blocking
	}

	session.onUpstreamCallAck = func(ack *message.UpstreamCallAck) {
		c.upstreamCallAckMu.Lock()
		ch, ok := c.upstreamCallAckCh[ack.CallID]
		if !ok {
			c.upstreamCallAckMu.Unlock()
			return
		}
		delete(c.upstreamCallAckCh, ack.CallID)
		c.upstreamCallAckMu.Unlock()

		ch <- ack // nonblocking
	}
}

// subscribeDownstreamMetadata registers a per-filter metadata subscription on wireConn
// (a snapshot obtained by the caller) and returns them bundled into a single channel.
func (c *Conn) subscribeDownstreamMetadata(ctx context.Context, wireConn *protocolSession, alias uint32, filters []*message.DownstreamFilter) (<-chan *message.DownstreamMetadata, error) {
	orDone := func(inCh <-chan *message.DownstreamMetadata) <-chan *message.DownstreamMetadata {
		resCh := make(chan *message.DownstreamMetadata)
		go func() {
			defer close(resCh)
			for {
				select {
				case v := <-inCh:
					resCh <- v
				case <-wireConn.Closed():
					return
				}
			}
		}()
		return resCh
	}
	resCh := make(chan *message.DownstreamMetadata, 1024)
	var wg sync.WaitGroup
	for _, filter := range filters {
		metaCh, err := wireConn.SubscribeDownstreamMeta(ctx, alias, filter.SourceNodeID)
		if err != nil {
			return nil, err
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			for v := range orDone(metaCh) {
				select {
				case resCh <- v:
				default:
				}
			}
		}()
	}
	go func() {
		defer close(resCh)
		wg.Wait()
	}()

	return resCh, nil
}
