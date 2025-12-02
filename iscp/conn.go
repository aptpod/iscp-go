package iscp

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/aptpod/iscp-go/errors"
	"github.com/aptpod/iscp-go/internal/retry"

	"github.com/aptpod/iscp-go/log"
	"github.com/aptpod/iscp-go/message"
	"github.com/aptpod/iscp-go/transport"
	"github.com/aptpod/iscp-go/wire"
)

var (
	defaultPingTimeout  = time.Second
	defaultPingInterval = 10 * time.Second

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
	if c.PingTimeout.Seconds() == 0 {
		c.PingTimeout = defaultPingTimeout
	}
	if c.PingInterval.Seconds() == 0 {
		c.PingInterval = defaultPingInterval
	}

	wireConn, err := c.connectWire()
	if err != nil {
		return nil, errors.Errorf("failed to connect wire: %w", err)
	}
	conn := &Conn{
		wireConn:              wireConn,
		downstreamIDGenerator: wire.NewAliasGenerator(1),
		replyCallChs:          make(map[string]chan *message.DownstreamCall),
		downstreamCallCh:      make(chan *message.DownstreamCall, 1024),
		replyCallCh:           make(chan *message.DownstreamCall, 1024),
		upstreamCallAckCh:     make(map[string]chan *message.UpstreamCallAck),
		upstreamRepository:    c.upstreamRepository,
		downstreamRepository:  c.downstreamRepository,
		eventDispatcher:       newEventDispatcher(),

		upstreams:   make(map[*Upstream]struct{}),
		downstreams: make(map[*Downstream]struct{}),

		logger:      c.Logger,
		sentStorage: c.sentStorage,
		state:       newConnState(),

		Config: *c,
	}

	go func() {
		for {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go func() {
				conn.state.WaitUntil(ctx, connStatusClosed)
				cancel()
				conn.eventDispatcher.cond.Broadcast()
			}()
			go func() {
				conn.eventDispatcher.dispatchLoop(ctx)
			}()

			if err := conn.run(ctx); err != nil {
				if err := conn.reconnect(ctx); err != nil {
					if errors.Is(err, errors.ErrConnectionClosed) {
						conn.logger.Warnf(ctx, "failed to reconnect: %+v", err)
						return
					}
					conn.logger.Errorf(ctx, "failed to reconnect: %+v", err)
					return
				}
				conn.Config.ReconnectedEventHandler.OnReconnected(&ReconnectedEvent{
					Config: conn.Config,
				})
				continue
			}
			return
		}
	}()

	return conn, nil
}

// Connは、iSCPのコネクションです。
type Conn struct {
	wireConnMu            sync.Mutex
	wireConn              *wire.ClientConn
	downstreamIDGenerator *wire.AliasGenerator

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

	sentStorage sentStorage

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
	err := c.send(ctx, func(ctx context.Context) error {
		c.wireConnMu.Lock()
		defer c.wireConnMu.Unlock()
		r, err := c.wireConn.SendUpstreamOpenRequest(ctx, &message.UpstreamOpenRequest{
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

	c.wireConnMu.Lock()
	ch, err := c.wireConn.SubscribeUpstreamChunkAck(ctx, resp.AssignedStreamIDAlias)
	c.wireConnMu.Unlock()
	if err != nil {
		return nil, errors.Errorf("failed to SubscribeUpstreamChunkAck: %w", err)
	}

	revDataIDAliases := make(map[message.DataID]uint32)
	for k, v := range resp.DataIDAliases {
		revDataIDAliases[*v] = k
	}

	// sentStorageの選択: Connレベルで設定されていればそれを使用、未設定ならQoSに応じて作成
	var storage sentStorage
	switch {
	case c.sentStorage != nil:
		storage = c.sentStorage
	case upconf.QoS == message.QoSReliable:
		storage = newInmemSentStorage() // Payloadを保存
	default:
		// QoSUnreliable または QoSPartial の場合
		storage = newInmemSentStorageNoPayload() // Payloadを保存しない
	}

	ctx, cancel := context.WithCancel(context.Background())
	u := &Upstream{
		ctx:              ctx,
		cancel:           cancel,
		ID:               resp.AssignedStreamID,
		dataIDAliases:    resp.DataIDAliases,
		revDataIDAliases: revDataIDAliases,
		ServerTime:       resp.ServerTime,
		idAlias:          resp.AssignedStreamIDAlias,
		wireConn:         c.wireConn,
		sequence:         newSequenceNumberGenerator(0),
		logger:           c.logger,

		ackCh:        ch,
		dpgCh:        make(chan *DataPointGroup),
		sent:         storage,
		resCh:        make(chan []*message.UpstreamChunkResult, 8),
		aliasCh:      make(chan map[uint32]*message.DataID, 8),
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
		receivedAck:            sync.NewCond(&sync.RWMutex{}),
	}
	go func() {
		defer c.state.cond.Broadcast()
		defer u.state.cond.Broadcast()
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
		context.AfterFunc(ctx, func() {
			u.eventDispatcher.cond.Broadcast()
		})
		var isResume bool
		for {
			if err := u.run(isResume); err != nil {
				if c.isClosed() {
					return
				}
				if err := c.state.WaitUntil(ctx, connStatusConnected); err != nil {
					u.logger.Errorf(ctx, "failed to wait state in resume upstream: %+v", err)
					return
				}

				if err := u.resume(c.wireConn); err != nil {
					u.logger.Errorf(ctx, "failed to resume upstream: %+v", err)
					return
				}
				u.logger.Infof(ctx, "Succeeded in resuming upstream %v", u.ID.String())
				isResume = true
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
		aliasGenerator = wire.NewAliasGenerator(0)
		aliases        = make(map[uint32]*message.DataID, len(downconf.DataIDs))
		revAliases     = make(map[message.DataID]uint32, len(downconf.DataIDs))
	)
	for _, v := range downconf.DataIDs {
		aliases[aliasGenerator.Next()] = v
		revAliases[*v] = aliasGenerator.CurrentValue()
	}
	alias := c.downstreamIDGenerator.Next()

	err = c.send(ctx, func(ctx context.Context) error {
		c.wireConnMu.Lock()
		dpsCh, err = c.wireConn.SubscribeDownstreamChunk(ctx, alias, downconf.QoS)
		c.wireConnMu.Unlock()
		if err != nil {
			return errors.Errorf("failed SubscribeDownstreamChunk: %w", err)
		}
		c.wireConnMu.Lock()
		ackCompCh, err = c.wireConn.SubscribeDownstreamChunkAckComplete(ctx, alias)
		c.wireConnMu.Unlock()
		if err != nil {
			return errors.Errorf("failed SubscribeDownstreamChunkAckComplete: %w", err)
		}

		metaCh, err = c.subscribeDownstreamMetadata(ctx, alias, filters)
		if err != nil {
			return errors.Errorf("failed subscribeDownstreamMetadata: %w", err)
		}

		resp, err = c.wireConn.SendDownstreamOpenRequest(ctx, &message.DownstreamOpenRequest{
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
		wireConn:                    c.wireConn,
		idAlias:                     alias,
		dpsCh:                       dpsCh,
		ackCompCh:                   ackCompCh,
		metaCh:                      metaCh,
		dataPointsCh:                make(chan *message.DownstreamChunk, 1024),
		metadataCh:                  make(chan *message.DownstreamMetadata, 1024),

		dataIDAliasGenerator:       aliasGenerator,
		upstreamInfoAliasGenerator: wire.NewAliasGenerator(0),

		ackFlushInterval:      *downconf.AckFlushInterval,
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
	}
	go func() {
		defer c.state.cond.Broadcast()
		defer down.state.cond.Broadcast()
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
		context.AfterFunc(ctx, func() {
			down.eventDispatcher.cond.Broadcast()
		})

		for {
			if err := down.run(); err != nil {
				if c.isClosed() {
					return
				}
				c.logger.Infof(ctx, "Wait until connected... downstreamID:[%s]", down.ID)
				if err := c.state.WaitUntil(ctx, connStatusConnected); err != nil {
					down.logger.Errorf(ctx, "Failed to wait state in resume downstream: %+v", err)
					return
				}

				if err := down.resume(c); err != nil {
					down.logger.Errorf(ctx, "Failed to resume downstream: %+v", err)
					return
				}
				down.logger.Infof(ctx, "Succeeded in resuming downstream [%v]", down.ID)
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
		c.wireConnMu.Lock()
		defer c.wireConnMu.Unlock()
		resp, err := c.wireConn.SendUpstreamMetadata(ctx, upmeta)
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

// sendは、メッセージ送信の再接続を考慮したラッパー関数。
//
// `f` で ワイヤ層のConnを使用して、メッセージをサーバーへ送信することを想定している。
// `f` が `ErrConnectionClosed` エラー（またはその派生エラー） を返却した場合は、再接続完了まで待機し、再接続完了後リトライを試みる
// その間、Connectionが明示的に閉じられた場合は `ErrConnectionClosed` を返却する。
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

func (c *Conn) observeConnClose(ctx context.Context) error {
	for {
		select {
		case <-c.wireConn.Closed():
			return errors.New("unexpected disconnected")
		case <-ctx.Done():
			return nil
		}
	}
}

func (c *Conn) reconnect(ctx context.Context) error {
	c.wireConnMu.Lock()
	defer c.wireConnMu.Unlock()
	if !c.state.CompareAndSwapNot(connStatusClosed, connStatusReconnecting) {
		return errors.ErrConnectionClosed
	}
	c.wireConn.Close()

	oc := c.Config
	if oc.PingTimeout.Seconds() == 0 {
		oc.PingTimeout = defaultPingTimeout
	}
	if oc.PingInterval.Seconds() == 0 {
		oc.PingInterval = defaultPingInterval
	}

	var res *wire.ClientConn
	var resErr error
	retry.Do(func() (end bool) {
		c.logger.Infof(ctx, "Try reconnecting...")

		res, resErr = c.Config.connectWire()
		if resErr != nil {
			return c.state.Is(connStatusClosed)
		}
		c.logger.Infof(ctx, "Reconnected")
		return true
	})
	if err := resErr; err != nil {
		return resErr
	}
	c.wireConn = res
	if !c.state.CompareAndSwap(connStatusReconnecting, connStatusConnected) {
		panic(errors.Errorf("unexpected error: expected reconnecting but %v", c.state.current))
	}
	return nil
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

func (c *Conn) close(ctx context.Context, msg *message.Disconnect) error {
	if c.state.Swap(connStatusClosed) != connStatusClosed {
		c.saveAndClearAllUpstreams(ctx)
		c.saveAndClearAllDownstreams(ctx)
	}

	c.wireConnMu.Lock()
	defer c.wireConnMu.Unlock()
	if err := c.wireConn.SendDisconnect(ctx, msg); err != nil {
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
	c.wireConnMu.Lock()
	defer c.wireConnMu.Unlock()
	return c.wireConn.UnderlyingTransport()
}

func (c *Conn) run(ctx context.Context) error {
	defer c.Config.DisconnectedEventHandler.OnDisconnected(&DisconnectedEvent{
		Config: c.Config,
	})
	eg, ctx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		c.state.WaitUntilOrClosed(ctx, connStatusReconnecting)
		if c.state.Is(connStatusClosed) {
			return nil
		}
		return errors.New("unexpected transport closed")
	})

	eg.Go(func() error {
		return c.readDownstreamCallLoop(ctx)
	})

	eg.Go(func() error {
		return c.readUpstreamCallAckLoop(ctx)
	})

	eg.Go(func() error {
		err := c.observeConnClose(ctx)
		if err != nil && !c.state.Is(connStatusClosed) {
			return err
		}
		return nil
	})
	if err := eg.Wait(); err != nil {
		return fmt.Errorf("unexpected disconnect: %w", err)
	}
	return nil
}

func (c *Conn) readUpstreamCallAckLoop(ctx context.Context) error {
	for {
		ack, err := c.wireConn.ReceiveUpstreamCallAck(ctx)
		if err != nil {
			if !errors.Is(err, context.Canceled) && !errors.Is(err, errors.ErrConnectionClosed) {
				c.logger.Warnf(ctx, "failed to ReceiveUpstreamCallAck: %+v", err)
			}
			return nil
		}
		c.upstreamCallAckMu.Lock()
		ch, ok := c.upstreamCallAckCh[ack.CallID]
		if !ok {
			c.upstreamCallAckMu.Unlock()
			continue
		}
		delete(c.upstreamCallAckCh, ack.CallID)
		c.upstreamCallAckMu.Unlock()

		ch <- ack // nonblocking
	}
}

func (c *Conn) readDownstreamCallLoop(ctx context.Context) error {
	for {
		dc, err := c.wireConn.ReceiveDownstreamCall(ctx)
		if err != nil {
			if !errors.Is(err, context.Canceled) && !errors.Is(err, errors.ErrConnectionClosed) {
				c.logger.Warnf(ctx, "failed to ReceiveDownstreamCall: %+v", err)
			}
			return nil
		}
		// request call
		if dc.RequestCallID == "" {
			select {
			case c.downstreamCallCh <- dc:
			default:
				c.logger.Warnf(ctx, "Discarded a e2e downstream call %+v", dc)
			}
			continue
		}
		// reply call
		select {
		case c.replyCallCh <- dc:
		default:
			c.logger.Warnf(ctx, "Discarded a e2e reply call %+v", dc)
		}
		c.replyCallsChsMu.Lock()
		ch, ok := c.replyCallChs[dc.RequestCallID]
		if !ok {
			c.replyCallsChsMu.Unlock()
			c.logger.Warnf(ctx, "No reply for request call id: %v", dc.RequestCallID)
			continue
		}
		delete(c.replyCallChs, dc.RequestCallID)
		c.replyCallsChsMu.Unlock()

		ch <- dc // non blocking
	}
}

func (c *Conn) subscribeDownstreamMetadata(ctx context.Context, alias uint32, filters []*message.DownstreamFilter) (<-chan *message.DownstreamMetadata, error) {
	wireConn := c.wireConn
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
