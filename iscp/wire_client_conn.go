package iscp

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/aptpod/iscp-go/errors"

	uuid "github.com/google/uuid"
	"golang.org/x/mod/semver"

	"github.com/aptpod/iscp-go/log"
	"github.com/aptpod/iscp-go/message"
	"github.com/aptpod/iscp-go/transport"
)

var (
	errNoSubscribeChannel = errors.New("no subscribe channel")
	defaultPingInterval   = 10 * time.Second
	defaultPingTimeout    = time.Second

	defaultPingIntervalForServer = 10 * time.Second
	defaultPingTimeoutForServer  = time.Second

	// ErrUnsupportedProtocolVersion は、サーバーが返したプロトコルバージョンがサポートされていない場合のエラーです。
	ErrUnsupportedProtocolVersion = errors.New("unsupported protocol version")

	// minAcceptableVersion は、受け入れ可能な最小プロトコルバージョンです（この値を含む）。
	minAcceptableVersion = "v2.0.0"
	// maxAcceptableVersion は、受け入れ可能な最大プロトコルバージョンです（この値を含まない）。
	maxAcceptableVersion = "v5.0.0"
	// resumeTokenMinVersion は、ResumeTokenをサポートする最小プロトコルバージョンです。
	resumeTokenMinVersion = "v3.0.0"
	// pingPongMinVersion は、iSCP Ping/Pongを使用しない最小プロトコルバージョンです。
	// この値以上ではトランスポートレベルのハートビートを使用します。
	pingPongMinVersion = "v4.0.0"
)

// protocolSessionは、Client側のコネクションです。
type protocolSession struct {
	transport           *transport.MessageTransport
	unreliableTransport *transport.MessageTransport

	idGenerator IDGenerator

	ctx    context.Context
	cancel context.CancelFunc

	msgRequestCh                    chan message.Request
	msgUpstreamChunkAckCh           chan *message.UpstreamChunkAck
	msgDownstreamChunkCh            chan *message.DownstreamChunk
	msgDownstreamChunkUnreliableCh  chan *message.DownstreamChunk
	msgDownstreamChunkAckCompleteCh chan *message.DownstreamChunkAckComplete
	msgDownstreamMetaDataCh         chan *message.DownstreamMetadata

	// onDownstreamCall は、DownstreamCallメッセージ受信時に呼び出されるコールバックです。
	onDownstreamCall func(*message.DownstreamCall)
	// onUpstreamCallAck は、UpstreamCallAckメッセージ受信時に呼び出されるコールバックです。
	onUpstreamCallAck func(*message.UpstreamCallAck)

	mu      sync.Mutex
	replyCh map[uint32]chan message.Request

	logger log.Logger

	protocolVersion string
	nodeID          string
	pingInterval    time.Duration
	pingTimeout     time.Duration

	upstreams              *clientUpstreams
	downstreams            *clientDownstreams
	accessToken            string
	intdashExtensionFields *intdashExtensionFields
}

// intdashExtensionFieldsは、intdash API用の拡張フィールドです。
type intdashExtensionFields message.IntdashExtensionFields

type clientUpstreams struct {
	mu             *sync.RWMutex
	acks           map[uint32]chan *message.UpstreamChunkAck
	aliases        map[uuid.UUID]uint32
	messageWriters map[uint32]*transport.MessageTransport
}

type clientDownstreams struct {
	mu            *sync.RWMutex
	dps           map[uint32]chan *message.DownstreamChunk
	dpsUnreliable map[uint32]chan *message.DownstreamChunk
	ackCompletes  map[uint32]chan *message.DownstreamChunkAckComplete
	metadata      map[uint32]map[string]chan *message.DownstreamMetadata
	aliases       map[uuid.UUID]uint32
}

// protocolSessionConfigは、クライアントコネクションの設定です。
type protocolSessionConfig struct {
	// Transportはトランスポートです。
	Transport *transport.MessageTransport

	// TransportはUnreliableなトランスポートです。nilの場合、QoSがUnreliableの時、Reliableなトランスポートを使用します。
	UnreliableTransport *transport.MessageTransport

	// Loggerはロガーです。
	Logger log.Logger

	// ProtocolVersionはサポートするプロトコルのバージョンです。
	ProtocolVersion string

	// NodeIDは、クライアントコネクションを開くノードのIDです。
	NodeID string

	// アクセストークンは、iSCP接続時に使用するアクセストークンです。
	AccessToken string

	// IntdashExtensionFieldsは、intdash APIの拡張フィールドです。
	IntdashExtensionFields *intdashExtensionFields

	// PingIntervalは、iSCPのPingメッセージを送信する間隔です。
	PingInterval time.Duration

	// PingTimeoutは、iSCPのPing送信後Pongが返却されるまでのタイムアウトです。
	//
	// タイムアウトした場合、iSCPのコネクションを切断します。
	PingTimeout time.Duration

	// OnDownstreamCallは、DownstreamCallメッセージ受信時に呼び出されるコールバックです。
	OnDownstreamCall func(*message.DownstreamCall)

	// OnUpstreamCallAckは、UpstreamCallAckメッセージ受信時に呼び出されるコールバックです。
	OnUpstreamCallAck func(*message.UpstreamCallAck)
}

// newProtocolSessionは、iSCP接続を行いprotocolSessionを返却します。
func newProtocolSession(c *protocolSessionConfig) (*protocolSession, error) {
	if c.Logger == nil {
		c.Logger = log.NewNop()
	}

	pingIntervalClient := c.PingInterval
	pingTimeoutClient := c.PingTimeout
	pingIntervalServer := c.PingInterval
	pingTimeoutServer := c.PingTimeout

	if pingIntervalClient.Seconds() == 0 {
		pingIntervalClient = defaultPingInterval
		pingIntervalServer = defaultPingIntervalForServer
	}
	if pingTimeoutClient.Seconds() == 0 {
		pingTimeoutClient = defaultPingTimeout
		pingTimeoutServer = defaultPingTimeoutForServer
	}

	ctx, cancel := context.WithCancel(context.Background())
	conn := &protocolSession{
		transport:                       c.Transport,
		unreliableTransport:             c.UnreliableTransport,
		idGenerator:                     newRequestIDGeneratorForClient(),
		ctx:                             ctx,
		cancel:                          cancel,
		msgRequestCh:                    make(chan message.Request, 8),
		msgUpstreamChunkAckCh:           make(chan *message.UpstreamChunkAck, 8),
		msgDownstreamChunkUnreliableCh:  make(chan *message.DownstreamChunk, 8),
		msgDownstreamChunkCh:            make(chan *message.DownstreamChunk, 8),
		msgDownstreamChunkAckCompleteCh: make(chan *message.DownstreamChunkAckComplete, 8),
		msgDownstreamMetaDataCh:         make(chan *message.DownstreamMetadata, 8),
		onDownstreamCall:                c.OnDownstreamCall,
		onUpstreamCallAck:               c.OnUpstreamCallAck,
		mu:                              sync.Mutex{},
		replyCh:                         make(map[uint32]chan message.Request),
		logger:                          c.Logger,
		protocolVersion:                 c.ProtocolVersion,
		nodeID:                          c.NodeID,
		accessToken:                     c.AccessToken,
		intdashExtensionFields:          c.IntdashExtensionFields,
		pingInterval:                    pingIntervalClient,
		pingTimeout:                     pingTimeoutClient,
		upstreams: &clientUpstreams{
			mu:             &sync.RWMutex{},
			acks:           make(map[uint32]chan *message.UpstreamChunkAck),
			aliases:        make(map[uuid.UUID]uint32),
			messageWriters: make(map[uint32]*transport.MessageTransport),
		},
		downstreams: &clientDownstreams{
			mu:            &sync.RWMutex{},
			dps:           make(map[uint32]chan *message.DownstreamChunk),
			dpsUnreliable: make(map[uint32]chan *message.DownstreamChunk),
			aliases:       make(map[uuid.UUID]uint32),
			ackCompletes:  make(map[uint32]chan *message.DownstreamChunkAckComplete),
			metadata:      make(map[uint32]map[string]chan *message.DownstreamMetadata),
		},
	}

	msg, err := conn.waitForConnected(pingIntervalServer, pingTimeoutServer)
	if err != nil {
		if !errors.Is(err, transport.ErrAlreadyClosed) {
			conn.logger.Errorf(ctx, "occurred in waitForConnected: %+v", err)
		}
		return nil, err
	}
	switch msg.ResultCode {
	case message.ResultCodeAuthFailed:
		return nil, ErrUnauthorized
	case message.ResultCodeSucceeded:
		if !isAcceptableProtocolVersion(msg.ProtocolVersion) {
			return nil, errors.Errorf("%w: server returned %s", ErrUnsupportedProtocolVersion, msg.ProtocolVersion)
		}
		conn.protocolVersion = msg.ProtocolVersion
		go conn.runWire()
		return conn, nil
	default:
		return nil, errors.FailedMessageError{
			ResultCode:      msg.ResultCode,
			ResultString:    msg.ResultString,
			ReceivedMessage: msg,
		}
	}
}

// Closedは、protocolSessionがクローズしているかどうか確認するためのチャンネルを返却します。
//
// protocolSessionがクローズしている場合、チャンネルは閉じられています。
func (c *protocolSession) Closed() <-chan struct{} {
	return c.ctx.Done()
}

// ProtocolVersion は、サーバーが返したプロトコルバージョンを返却します。
func (c *protocolSession) ProtocolVersion() string {
	return c.protocolVersion
}

// SupportsResumeToken は、現在の接続がResumeTokenをサポートしているかどうかを返します。
func (c *protocolSession) SupportsResumeToken() bool {
	v := "v" + c.protocolVersion
	return semver.Compare(v, resumeTokenMinVersion) >= 0
}

func (c *protocolSession) runWire() {
	defer c.cancel()

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		c.readReliableLoop()
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		c.readUnreliableLoop()
	}()

	if c.needsPingPong() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.keepAliveLoop()
		}()
	}

	wg.Wait()
}

func (c *protocolSession) readReliableLoop() {
	defer close(c.msgRequestCh)
	defer close(c.msgUpstreamChunkAckCh)
	defer close(c.msgDownstreamChunkCh)
	defer close(c.msgDownstreamChunkAckCompleteCh)
	defer close(c.msgDownstreamMetaDataCh)

	go c.readRequestLoop()
	go c.readUpstreamChunkAckLoop()
	go c.readDownstreamChunkLoop()
	go c.readDownstreamChunkAckCompleteLoop()
	go c.readDownstreamMetadataLoop()

	msgCh := make(chan message.Message)
	go func() {
		defer close(msgCh)
		for {
			msg, err := c.transport.ReadMessage()
			if err != nil {
				if !errors.Is(err, transport.ErrAlreadyClosed) &&
					!errors.Is(err, transport.EOF) &&
					!errors.Is(err, errors.ErrConnectionClose) &&
					!errors.Is(err, net.ErrClosed) {
					c.logger.Errorf(c.ctx, "occurred in transport.ReadMessage: %+v", err)
				}
				return
			}
			select {
			case msgCh <- msg:
			case <-c.ctx.Done():
				return
			}
		}
	}()

	for msg := range msgCh {
		switch m := msg.(type) {
		case *message.Ping:
			// Pongをインラインで送信（別ゴルーチンでブロッキングを回避）
			go func() {
				if err := c.transport.WriteMessage(&message.Pong{
					RequestID: m.RequestID,
				}); err != nil {
					if !errors.Is(err, transport.ErrAlreadyClosed) &&
						!errors.Is(err, transport.EOF) &&
						!errors.Is(err, errors.ErrConnectionClose) &&
						!errors.Is(err, net.ErrClosed) {
						c.logger.Errorf(c.ctx, "%+v", err)
					}
				}
			}()
		case message.Request:
			c.msgRequestCh <- m
		case *message.Disconnect:
			// Disconnectをインラインで処理
			c.logger.Warnf(c.ctx, "received disconnect: %s", m.ResultString)
			if err := c.transport.Close(); err != nil {
				if !errors.Is(err, transport.ErrAlreadyClosed) {
					c.logger.Errorf(c.ctx, "%+v", err)
				}
			}
			return
		case *message.UpstreamChunkAck:
			c.msgUpstreamChunkAckCh <- m
		case *message.DownstreamChunk:
			c.msgDownstreamChunkCh <- m
		case *message.DownstreamChunkAckComplete:
			c.msgDownstreamChunkAckCompleteCh <- m
		case *message.DownstreamMetadata:
			c.msgDownstreamMetaDataCh <- m
		case *message.DownstreamCall:
			if c.onDownstreamCall != nil {
				c.onDownstreamCall(m)
			}
		case *message.UpstreamCallAck:
			if c.onUpstreamCallAck != nil {
				c.onUpstreamCallAck(m)
			}
		default:
			// TODO invalid message
		}
	}
}

func (c *protocolSession) readUnreliableLoop() {
	go c.readDownstreamChunkUnreliableLoop()
	defer close(c.msgDownstreamChunkUnreliableCh)

	if c.unreliableTransport == nil {
		return
	}

	tr := c.unreliableTransport
	msgCh := make(chan message.Message, 1024)
	go func() {
		defer close(msgCh)
		for {
			msg, err := tr.ReadMessage()
			if err != nil {
				if !errors.Is(err, transport.ErrAlreadyClosed) &&
					!errors.Is(err, transport.EOF) &&
					!errors.Is(err, errors.ErrConnectionClose) &&
					!errors.Is(err, net.ErrClosed) {
					c.logger.Errorf(c.ctx, "occurred in transport.ReadMessage: %+v", err)
				}
				return
			}
			select {
			case msgCh <- msg:
			case <-c.ctx.Done():
				return
			}
		}
	}()

	for msg := range msgCh {
		switch m := msg.(type) {
		case *message.DownstreamChunk:
			c.msgDownstreamChunkUnreliableCh <- m
		default:
			// todo invalid message
		}
	}
}

// Closeは、クライアント接続を閉じます。
func (c *protocolSession) Close() error {
	c.cancel()
	return c.transport.Close()
}

// UnderlyingTransport は内部で使用しているトランスポートを返します。
func (c *protocolSession) UnderlyingTransport() transport.ReadWriter {
	return c.transport.UnderlyingTransport()
}

// SendDisconnectは、Disconnectメッセージを送信します。
func (c *protocolSession) SendDisconnect(ctx context.Context, msg *message.Disconnect) error {
	return c.transport.WriteMessage(msg)
}

// SendUpstreamMetadataは、UpstreamMetadataを送信します。
func (c *protocolSession) SendUpstreamMetadata(ctx context.Context, msg *message.UpstreamMetadata) (*message.UpstreamMetadataAck, error) {
	msg.RequestID = message.RequestID(c.idGenerator.Next())
	res, err := c.sendRequest(ctx, msg)
	if err != nil {
		return nil, err
	}
	return res.(*message.UpstreamMetadataAck), nil
}

// SubscribeUpstreamChunkAckは、UpstreamChunkAckを待ち受けます。
func (c *protocolSession) SubscribeUpstreamChunkAck(ctx context.Context, alias uint32) (<-chan *message.UpstreamChunkAck, error) {
	c.upstreams.mu.Lock()
	defer c.upstreams.mu.Unlock()

	ch, ok := c.upstreams.acks[alias]
	if !ok {
		return nil, errNoSubscribeChannel
	}
	return ch, nil
}

func (c *protocolSession) openUpstream(ctx context.Context, qoS message.QoS, streamID uuid.UUID, streamIDAlias uint32) {
	c.upstreams.mu.Lock()
	defer c.upstreams.mu.Unlock()

	c.upstreams.aliases[streamID] = streamIDAlias

	ackCh := make(chan *message.UpstreamChunkAck, 1024)
	c.upstreams.acks[streamIDAlias] = ackCh

	switch qoS {
	case message.QoSReliable, message.QoSPartial:
		c.upstreams.messageWriters[streamIDAlias] = c.transport
	case message.QoSUnreliable:
		if c.unreliableTransport != nil {
			c.upstreams.messageWriters[streamIDAlias] = c.unreliableTransport
		} else {
			c.upstreams.messageWriters[streamIDAlias] = c.transport
		}
	default:
		// todo, unreachable
		panic("unsupported QoS")
	}
}

// SendUpstreamOpenRequestは、UpstreamOpenRequestを送信します。
func (c *protocolSession) SendUpstreamOpenRequest(ctx context.Context, req *message.UpstreamOpenRequest) (*message.UpstreamOpenResponse, error) {
	req.RequestID = message.RequestID(c.idGenerator.Next())
	resp, err := c.sendRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	res := resp.(*message.UpstreamOpenResponse)
	c.openUpstream(ctx, req.QoS, res.AssignedStreamID, res.AssignedStreamIDAlias)

	return res, nil
}

// SendUpstreamResumeRequestは、UpstreamResumeRequestを送信します。
func (c *protocolSession) SendUpstreamResumeRequest(ctx context.Context, req *message.UpstreamResumeRequest, qoS message.QoS) (*message.UpstreamResumeResponse, error) {
	id := c.idGenerator.Next()

	req.RequestID = message.RequestID(id)
	resp, err := c.sendRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	res := resp.(*message.UpstreamResumeResponse)

	c.openUpstream(ctx, qoS, req.StreamID, res.AssignedStreamIDAlias)

	return res, nil
}

// SendUpstreamChunkは、UpstreamChunkを送信します。
func (c *protocolSession) SendUpstreamChunk(ctx context.Context, req *message.UpstreamChunk) error {
	c.upstreams.mu.RLock()
	tr, ok := c.upstreams.messageWriters[req.StreamIDAlias]
	c.upstreams.mu.RUnlock()

	if !ok {
		return errors.New("stream not exist")
	}
	err := tr.WriteMessage(req)
	return err
}

// SendUpstreamCloseRequestは、UpstreamCloseRequestを送信します。
func (c *protocolSession) SendUpstreamCloseRequest(ctx context.Context, req *message.UpstreamCloseRequest) (*message.UpstreamCloseResponse, error) {
	req.RequestID = message.RequestID(c.idGenerator.Next())
	resp, err := c.sendRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	c.upstreams.mu.Lock()
	defer c.upstreams.mu.Unlock()
	alias, ok := c.upstreams.aliases[req.StreamID]
	if !ok {
		return resp.(*message.UpstreamCloseResponse), nil
	}

	delete(c.upstreams.aliases, req.StreamID)

	if _, ok = c.upstreams.acks[alias]; ok {
		delete(c.upstreams.acks, alias)
	}

	if _, ok = c.upstreams.messageWriters[alias]; ok {
		delete(c.upstreams.messageWriters, alias)
	}

	return resp.(*message.UpstreamCloseResponse), nil
}

// SubscribeDownstreamChunkは、指定したストリームIDエイリアス、QoSのDownstreamChunkを待ち受けます。
func (c *protocolSession) SubscribeDownstreamChunk(ctx context.Context, alias uint32, qoS message.QoS) (<-chan *message.DownstreamChunk, error) {
	switch qoS {
	case message.QoSReliable, message.QoSPartial:
		return c.subscribeDownstreamChunk(ctx, alias)
	case message.QoSUnreliable:
		return c.subscribeDownstreamChunkUnreliable(ctx, alias)
	default:
		// todo, unreachable
		panic("unsupported QoS")
	}
}

func (c *protocolSession) newDownstreamChunkCh(alias uint32) (<-chan *message.DownstreamChunk, error) {
	c.downstreams.mu.Lock()
	defer c.downstreams.mu.Unlock()
	if _, ok := c.downstreams.dps[alias]; ok {
		return nil, errors.New("already subscribed")
	}
	ch := make(chan *message.DownstreamChunk, 1024)
	c.downstreams.dps[alias] = ch

	return ch, nil
}

func (c *protocolSession) newDownstreamChunkUnreliableCh(alias uint32) (<-chan *message.DownstreamChunk, error) {
	c.downstreams.mu.Lock()
	defer c.downstreams.mu.Unlock()
	if _, ok := c.downstreams.dpsUnreliable[alias]; ok {
		return nil, errors.New("already subscribed")
	}
	ch := make(chan *message.DownstreamChunk, 1024)
	c.downstreams.dpsUnreliable[alias] = ch
	return ch, nil
}

func (c *protocolSession) subscribeDownstreamChunk(ctx context.Context, alias uint32) (<-chan *message.DownstreamChunk, error) {
	return c.newDownstreamChunkCh(alias)
}

func (c *protocolSession) subscribeDownstreamChunkUnreliable(ctx context.Context, alias uint32) (<-chan *message.DownstreamChunk, error) {
	if c.unreliableTransport != nil {
		return c.newDownstreamChunkUnreliableCh(alias)
	} else {
		return c.newDownstreamChunkCh(alias)
	}
}

// SubscribeDownstreamChunkAckCompleteは、指定したストリームIDエイリアスのDownstreamChunkAckCompleteを待ち受けます。
func (c *protocolSession) SubscribeDownstreamChunkAckComplete(ctx context.Context, alias uint32) (<-chan *message.DownstreamChunkAckComplete, error) {
	c.downstreams.mu.Lock()
	defer c.downstreams.mu.Unlock()
	if _, ok := c.downstreams.ackCompletes[alias]; ok {
		return nil, errors.New("already subscribed")
	}
	ch := make(chan *message.DownstreamChunkAckComplete, 1024)
	c.downstreams.ackCompletes[alias] = ch
	return ch, nil
}

// SubscribeDownstreamMetaは、指定したストリームIDエイリアス、ノードIDのDownstreamMetadataを待ち受けます。
func (c *protocolSession) SubscribeDownstreamMeta(ctx context.Context, alias uint32, srcNodeID string) (<-chan *message.DownstreamMetadata, error) {
	c.downstreams.mu.Lock()
	defer c.downstreams.mu.Unlock()

	resCh := make(chan *message.DownstreamMetadata, 1024)
	if _, ok := c.downstreams.metadata[alias]; !ok {
		c.downstreams.metadata[alias] = make(map[string]chan *message.DownstreamMetadata)
	}
	c.downstreams.metadata[alias][srcNodeID] = resCh
	return resCh, nil
}

// SendDownstreamResumeRequestは、DownstreamResumeRequestを送信します。
func (c *protocolSession) SendDownstreamResumeRequest(ctx context.Context, req *message.DownstreamResumeRequest) (*message.DownstreamResumeResponse, error) {
	req.RequestID = message.RequestID(c.idGenerator.Next())
	res, err := c.sendRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	resp := res.(*message.DownstreamResumeResponse)

	c.downstreams.mu.Lock()
	defer c.downstreams.mu.Unlock()
	c.downstreams.aliases[req.StreamID] = req.DesiredStreamIDAlias

	return resp, nil
}

// SendDownstreamOpenRequestは、DownstreamOpenRequestを送信します。
func (c *protocolSession) SendDownstreamOpenRequest(ctx context.Context, req *message.DownstreamOpenRequest) (*message.DownstreamOpenResponse, error) {
	req.RequestID = message.RequestID(c.idGenerator.Next())
	res, err := c.sendRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	resp := res.(*message.DownstreamOpenResponse)

	c.downstreams.mu.Lock()
	defer c.downstreams.mu.Unlock()
	c.downstreams.aliases[resp.AssignedStreamID] = req.DesiredStreamIDAlias

	return resp, nil
}

// SendDownstreamCloseRequestは、DownstreamCloseRequestを送信します。
func (c *protocolSession) SendDownstreamCloseRequest(ctx context.Context, req *message.DownstreamCloseRequest) (*message.DownstreamCloseResponse, error) {
	req.RequestID = message.RequestID(c.idGenerator.Next())
	resp, err := c.sendRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	c.downstreams.mu.Lock()
	defer c.downstreams.mu.Unlock()

	alias, ok := c.downstreams.aliases[req.StreamID]
	if !ok {
		return resp.(*message.DownstreamCloseResponse), nil
	}
	delete(c.downstreams.aliases, req.StreamID)

	if _, ok = c.downstreams.dps[alias]; ok {
		delete(c.downstreams.dps, alias)
	}

	if _, ok = c.downstreams.dpsUnreliable[alias]; ok {
		delete(c.downstreams.dpsUnreliable, alias)
	}

	if _, ok = c.downstreams.ackCompletes[alias]; ok {
		delete(c.downstreams.ackCompletes, alias)
	}

	if _, ok = c.downstreams.metadata[alias]; ok {
		delete(c.downstreams.metadata, alias)
	}

	return resp.(*message.DownstreamCloseResponse), nil
}

// SendDownstreamDataPointsAckは、DownstreamMetadataAckを送信します。
func (c *protocolSession) SendDownstreamDataPointsAck(ctx context.Context, ack *message.DownstreamChunkAck) error {
	return c.transport.WriteMessage(ack)
}

// SendDownstreamMetadataAckは、DownstreamMetadataAckを送信します。
func (c *protocolSession) SendDownstreamMetadataAck(ctx context.Context, ack *message.DownstreamMetadataAck) error {
	return c.transport.WriteMessage(ack)
}

// SendUpstreamCallは、UpstreamCallを送信します。
func (c *protocolSession) SendUpstreamCall(ctx context.Context, call *message.UpstreamCall) error {
	return c.transport.WriteMessage(call)
}

func (c *protocolSession) sendRequest(ctx context.Context, req message.Request) (message.Request, error) {
	reply := make(chan message.Request, 1)
	c.mu.Lock()
	c.replyCh[req.GetRequestID()] = reply
	c.mu.Unlock()
	if err := c.transport.WriteMessage(req); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.ctx.Done():
		return nil, errors.ErrConnectionClosed
	case reply := <-reply:
		return reply, nil
	}
}

func (c *protocolSession) readRequestLoop() {
	for msg := range c.msgRequestCh {
		c.mu.Lock()
		replyCh, ok := c.replyCh[msg.GetRequestID()]
		if !ok {
			c.mu.Unlock()
			continue
		}

		delete(c.replyCh, msg.GetRequestID())
		c.mu.Unlock()
		replyCh <- msg // non blocking
	}
}

func (c *protocolSession) readUpstreamChunkAckLoop() {
	defer func() {
		for _, ackCh := range c.upstreams.acks {
			close(ackCh)
		}
	}()

	for msg := range c.msgUpstreamChunkAckCh {
		c.upstreams.mu.RLock()
		ch, ok := c.upstreams.acks[msg.StreamIDAlias]
		c.upstreams.mu.RUnlock()
		if !ok {
			continue
		}

		select {
		case ch <- msg:
		default:
		}
	}
}

// dispatchToStream is a generic dispatcher that routes messages from ch
// to per-stream subscriber channels looked up by alias.
func dispatchToStream[T any](ch <-chan T, mu *sync.RWMutex, getAlias func(T) uint32, getTarget func(uint32) (chan T, bool)) {
	for msg := range ch {
		alias := getAlias(msg)
		mu.RLock()
		target, ok := getTarget(alias)
		mu.RUnlock()
		if !ok {
			continue
		}
		select {
		case target <- msg:
		default:
		}
	}
}

func (c *protocolSession) readDownstreamChunkLoop() {
	dispatchToStream(c.msgDownstreamChunkCh, c.downstreams.mu,
		func(msg *message.DownstreamChunk) uint32 { return msg.StreamIDAlias },
		func(alias uint32) (chan *message.DownstreamChunk, bool) {
			ch, ok := c.downstreams.dps[alias]
			return ch, ok
		},
	)
}

func (c *protocolSession) readDownstreamChunkUnreliableLoop() {
	dispatchToStream(c.msgDownstreamChunkUnreliableCh, c.downstreams.mu,
		func(msg *message.DownstreamChunk) uint32 { return msg.StreamIDAlias },
		func(alias uint32) (chan *message.DownstreamChunk, bool) {
			ch, ok := c.downstreams.dpsUnreliable[alias]
			return ch, ok
		},
	)
}

func (c *protocolSession) readDownstreamChunkAckCompleteLoop() {
	dispatchToStream(c.msgDownstreamChunkAckCompleteCh, c.downstreams.mu,
		func(msg *message.DownstreamChunkAckComplete) uint32 { return msg.StreamIDAlias },
		func(alias uint32) (chan *message.DownstreamChunkAckComplete, bool) {
			ch, ok := c.downstreams.ackCompletes[alias]
			return ch, ok
		},
	)
}

func (c *protocolSession) readDownstreamMetadataLoop() {
	for msg := range c.msgDownstreamMetaDataCh {
		c.downstreams.mu.RLock()
		chs, ok := c.downstreams.metadata[msg.StreamIDAlias]
		if !ok {
			c.downstreams.mu.RUnlock()
			continue
		}
		ch, ok := chs[msg.SourceNodeID]
		c.downstreams.mu.RUnlock()
		if !ok {
			continue
		}
		select {
		case ch <- msg:
		default:
		}
	}
}

func (c *protocolSession) keepAliveLoop() {
	ticker := time.NewTicker(c.pingInterval)
	defer ticker.Stop()

	for {
		if _, err := c.sendPing(); err != nil {
			select {
			case <-c.ctx.Done():
				// already called close method
				return
			default:
			}
			c.logger.Warnf(c.ctx, "Ping timeout, disconnect :%v", err)
			c.Close()
			return
		}

		select {
		case <-ticker.C:
		case <-c.ctx.Done():
			return
		}
	}
}

func (c *protocolSession) sendPing() (*message.Pong, error) {
	ctx, cancel := context.WithTimeout(c.ctx, c.pingTimeout)
	defer cancel()
	resp, err := c.sendRequest(ctx, &message.Ping{
		RequestID: message.RequestID(c.idGenerator.Next()),
	})
	if err != nil {
		return nil, err
	}
	return resp.(*message.Pong), nil
}

// needsPingPong は、iSCP レベルの Ping/Pong keepalive が必要かどうかを返します。
// ProtocolVersion < 4.0.0 の場合に true を返します。
func (c *protocolSession) needsPingPong() bool {
	v := "v" + c.protocolVersion
	return semver.Compare(v, pingPongMinVersion) < 0
}

func (c *protocolSession) waitForConnected(pingInterval, pingTimeout time.Duration) (*message.ConnectResponse, error) {
	if pingInterval == 0 {
		pingInterval = defaultPingIntervalForServer
	}

	if pingTimeout == 0 {
		pingTimeout = defaultPingTimeoutForServer
	}
	if err := c.transport.WriteMessage(&message.ConnectRequest{
		RequestID: message.RequestID(c.idGenerator.Next()),

		PingInterval: pingInterval,
		PingTimeout:  pingTimeout,

		ProtocolVersion: c.protocolVersion,
		NodeID:          c.nodeID,
		ExtensionFields: &message.ConnectRequestExtensionFields{
			AccessToken: c.accessToken,
			Intdash:     (*message.IntdashExtensionFields)(c.intdashExtensionFields),
		},
	}); err != nil {
		return nil, err
	}
	msg, err := c.transport.ReadMessage()
	if err != nil {
		return nil, err
	}
	switch m := msg.(type) {
	case *message.ConnectResponse:
		return m, nil
	case *message.Disconnect:
		return nil, errors.Errorf("disconnected %s", m.ResultString)
	default:
		return nil, errors.Errorf("invalid message %T", msg)
	}
}

// isAcceptableProtocolVersion は、サーバーが返したプロトコルバージョンが受け入れ可能かどうかを判定します。
// 受け入れ可能なバージョン: v2.0.0 <= version < v2.2.0
func isAcceptableProtocolVersion(version string) bool {
	// semverパッケージは "v" プレフィックスが必要
	v := "v" + version
	if !semver.IsValid(v) {
		return false
	}
	// minAcceptableVersion <= version < maxAcceptableVersion
	return semver.Compare(v, minAcceptableVersion) >= 0 && semver.Compare(v, maxAcceptableVersion) < 0
}
