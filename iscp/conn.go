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
	if c.ReconnectedEventHandler == nil {
		c.ReconnectedEventHandler = nopReconnectedEventHandler{}
	}
	if c.DisconnectedEventHandler == nil {
		c.DisconnectedEventHandler = nopDisconnectedEventHandler{}
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

		logger: c.Logger,
		state:  newConnState(),

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

// snapshotWireConnは、現在のwireConnへの参照を短いロック区間で取得します。
//
// 返されたセッションへの呼び出しはロックを保持せずに行うこと（保持したまま
// ラウンドトリップすると、サーバーが応答しない間ロックが解放されず、Close等を
// 道連れにするため）。取得後に再接続でwireConnが差し替えられた場合、旧セッションは
// close済みなので呼び出しはErrConnectionClosedで失敗する。呼び出し側はc.send経由で
// 再試行するか、エラーとして返すこと。
func (c *Conn) snapshotWireConn() *wire.ClientConn {
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
	var wireConn *wire.ClientConn
	err := c.send(ctx, func(ctx context.Context) error {
		// スナップショットに対してロック外でラウンドトリップする。往復中に
		// 再接続で差し替えられた場合は ErrConnectionClosed が返り、c.send が
		// 再接続完了を待って新しいスナップショットで再試行する。
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

	// 以降は open のラウンドトリップに応答したセッション（スナップショット）を
	// 一貫して使う。ストリームの alias を知っているのはこのセッションであり、
	// 直後に再接続で差し替えられていた場合は購読・送信が ErrConnectionClosed で
	// 失敗し、resume 経路が新しいセッションで再購読する。
	ch, err := wireConn.SubscribeUpstreamChunkAck(ctx, resp.AssignedStreamIDAlias)
	if err != nil {
		return nil, errors.Errorf("failed to SubscribeUpstreamChunkAck: %w", err)
	}

	revDataIDAliases := make(map[message.DataID]uint32)
	for k, v := range resp.DataIDAliases {
		revDataIDAliases[*v] = k
	}

	// ResumeTokenの保存はプロトコルバージョンに応じて判定
	// v3.0.0以降: ResumeTokenをサポート（送受信・保存する）
	// v2.x.x: ResumeTokenを無視（空文字列で保存しない）
	var resumeToken string
	if wireConn.SupportsResumeToken() {
		resumeToken = resp.ResumeToken
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
		wireConn:         wireConn,
		sequence:         newSequenceNumberGenerator(0),
		logger:           c.logger,

		ackCh:        ch,
		dpgCh:        make(chan *DataPointGroup),
		sentBuf:      make(map[uint32]DataPointGroups),
		keepPayload:  upconf.QoS == message.QoSReliable,
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

		resumeToken: resumeToken,
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

				if err := u.resume(c.snapshotWireConn()); err != nil {
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

	var wireConn *wire.ClientConn
	err = c.send(ctx, func(ctx context.Context) error {
		// スナップショットに対してロック外で購読・ラウンドトリップする。
		// c.send による再試行時は閉じたセッションの購読を捨てて、新しい
		// スナップショットに対して購読からやり直す。
		wireConn = c.snapshotWireConn()
		dpsCh, err = wireConn.SubscribeDownstreamChunk(ctx, alias, downconf.QoS)
		if err != nil {
			return errors.Errorf("failed SubscribeDownstreamChunk: %w", err)
		}
		ackCompCh, err = wireConn.SubscribeDownstreamChunkAckComplete(ctx, alias)
		if err != nil {
			return errors.Errorf("failed SubscribeDownstreamChunkAckComplete: %w", err)
		}

		metaCh, err = c.subscribeDownstreamMetadata(ctx, alias, filters)
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
	if downconf.CloseTimeout == nil {
		downconf.CloseTimeout = &defaultCloseTimeout
	}

	// ResumeTokenの保存はプロトコルバージョンに応じて判定
	// v3.0.0以降: ResumeTokenをサポート（送受信・保存する）
	// v2.x.x: ResumeTokenを無視（空文字列で保存しない）
	var resumeToken string
	if wireConn.SupportsResumeToken() {
		resumeToken = resp.ResumeToken
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
		dataPointsCh:                make(chan *message.DownstreamChunk, 1024),
		metadataCh:                  make(chan *message.DownstreamMetadata, 1024),

		dataIDAliasGenerator:       aliasGenerator,
		upstreamInfoAliasGenerator: wire.NewAliasGenerator(0),

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

		resumeToken: resumeToken,
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
		// スナップショットに対してロック外でラウンドトリップする（再接続時は
		// c.send が新しいスナップショットで再試行する）。
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
	// wireConnMu の保持は「旧セッションのポインタ読み」と「新セッションの closed
	// 再確認 + 代入」の短い区間だけに限定し、旧セッションの close と dial は
	// ロック外で行う。dial や旧セッションの close がロック内でブロックすると、
	// Close() までロック待ちで道連れになるため（かつてはこれを TryLock +
	// ポーリングによるタイムアウトで救済していた）。
	c.wireConnMu.Lock()
	if !c.state.CompareAndSwapNot(connStatusClosed, connStatusReconnecting) {
		c.wireConnMu.Unlock()
		return errors.ErrConnectionClosed
	}
	old := c.wireConn
	c.wireConnMu.Unlock()
	old.Close()

	oc := c.Config
	if oc.PingTimeout.Seconds() == 0 {
		oc.PingTimeout = defaultPingTimeout
	}
	if oc.PingInterval.Seconds() == 0 {
		oc.PingInterval = defaultPingInterval
	}

	var res *wire.ClientConn
	var resErr error
	// ctx は Close() が state を connStatusClosed にした後、次のリトライ間隔の
	// スリープ中にも即座に打ち切られる。dial 自体は ctx を見ないため実行中の
	// 1回はブロックし続けることがあるが、ロック外なので Close() を妨げない。
	retry.DoWithContext(ctx, func() (end bool) {
		c.logger.Infof(ctx, "Try reconnecting...")

		res, resErr = c.Config.connectWire()
		if resErr != nil {
			return c.state.Is(connStatusClosed)
		}
		c.logger.Infof(ctx, "Reconnected")
		return true
	})
	if res == nil {
		// dial が一度も成功しないまま打ち切られた。resErr が nil のままなのは
		// ctx キャンセルにより f が一度も呼ばれなかった場合のみ。
		if resErr == nil || c.state.Is(connStatusClosed) {
			return errors.ErrConnectionClosed
		}
		return resErr
	}

	// Close() が dial 中に呼ばれていた場合、wireConn への代入前に検出して
	// 新セッションを閉じる（検出しないと、close() が既に完了していて誰も
	// res を閉じないままリークする）。判定と代入は close() と同じ wireConnMu
	// の下で行うため取りこぼしはない。
	c.wireConnMu.Lock()
	defer c.wireConnMu.Unlock()
	if c.state.Is(connStatusClosed) {
		res.Close()
		return errors.ErrConnectionClosed
	}
	c.wireConn = res
	if !c.state.CompareAndSwap(connStatusReconnecting, connStatusConnected) {
		// Close() が直前のIs()チェックとこのCompareAndSwapの間で状態をClosed
		// にした場合にここへ来る。c.wireConnには既にresが代入済みであり、
		// close()はこのwireConnMuの解放を待っているため、解放後にresを読んで
		// 閉じる（ここでres.Closeを呼ぶと二重closeになる）。
		return errors.ErrConnectionClosed
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

// disconnectSendTimeoutは、Close時にDisconnectメッセージの送信を待つ上限時間です。
// SendDisconnectはctxを無視して下層transportへWriteするため、下層transportが
// ブロックし続ける状況でもCloseが無期限にブロックしないようにするための猶予です。
const disconnectSendTimeout = 3 * time.Second

func (c *Conn) close(ctx context.Context, msg *message.Disconnect) error {
	if c.state.Swap(connStatusClosed) != connStatusClosed {
		c.saveAndClearAllUpstreams(ctx)
		c.saveAndClearAllDownstreams(ctx)
	}

	// wireConnMu の保持区間:
	//   - reconnect: ポインタ読みとポインタ swap のみ（旧セッションの close と
	//     dial はロック外）→ 有界
	//   - OpenUpstream / OpenDownstream / SendMetadata:
	//     スナップショット取得のみ（ラウンドトリップはロック外）→ 有界
	//   - close 自身: Disconnect 送信は別goroutine + selectで
	//     disconnectSendTimeoutが上限
	// したがって素の Lock() の待ちは有界保持の合成で有界であり、タイムアウト付き
	// の取得（かつての TryLock + ポーリング）は不要。
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

// subscribeDownstreamMetadataは、wireConnのスナップショットに対してフィルタごとの
// メタデータ購読を登録し、1本のチャネルに束ねて返します。
//
// downstream.go の resume() からも呼ばれるため、呼び出し側にスナップショットを
// 引き回させず、ここで短いロック区間から取得する。
func (c *Conn) subscribeDownstreamMetadata(ctx context.Context, alias uint32, filters []*message.DownstreamFilter) (<-chan *message.DownstreamMetadata, error) {
	wireConn := c.snapshotWireConn()
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
