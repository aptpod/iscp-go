package iscp

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/aptpod/iscp-go/v2/errors"

	uuid "github.com/google/uuid"
	"golang.org/x/mod/semver"

	"github.com/aptpod/iscp-go/v2/log"
	"github.com/aptpod/iscp-go/v2/message"
	"github.com/aptpod/iscp-go/v2/transport"
)

var (
	errNoSubscribeChannel = errors.New("no subscribe channel")
	defaultPingInterval   = 10 * time.Second
	defaultPingTimeout    = time.Second

	// ErrUnsupportedProtocolVersion は、サーバーが返したプロトコルバージョンがサポートされていない場合のエラーです。
	ErrUnsupportedProtocolVersion = errors.New("unsupported protocol version")

	// ErrUnauthorized は、認証されていないときに返されます。
	ErrUnauthorized = errors.Errorf("unauthorized : %w", ErrInvalidConnectRequest)

	// ErrInvalidConnectRequest は、ConnectRequestが不正の場合に返されます。
	ErrInvalidConnectRequest = errors.New("invalid connect request")

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

// isTransportCloseError は、エラーがトランスポートの正常クローズに起因するものかどうかを判定します。
func isTransportCloseError(err error) bool {
	return errors.Is(err, transport.ErrAlreadyClosed) ||
		errors.Is(err, transport.EOF) ||
		errors.Is(err, errors.ErrConnectionClose) ||
		errors.Is(err, net.ErrClosed)
}

// protocolSessionは、Client側のコネクションです。
type protocolSession struct {
	transport           *transport.MessageTransport
	unreliableTransport *transport.MessageTransport

	idGenerator IDGenerator

	ctx    context.Context
	cancel context.CancelFunc

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
		pingIntervalServer = defaultPingInterval
	}
	if pingTimeoutClient.Seconds() == 0 {
		pingTimeoutClient = defaultPingTimeout
		pingTimeoutServer = defaultPingTimeout
	}

	ctx, cancel := context.WithCancel(context.Background())
	conn := &protocolSession{
		transport:              c.Transport,
		unreliableTransport:    c.UnreliableTransport,
		idGenerator:            newRequestIDGeneratorForClient(),
		ctx:                    ctx,
		cancel:                 cancel,
		onDownstreamCall:       c.OnDownstreamCall,
		onUpstreamCallAck:      c.OnUpstreamCallAck,
		mu:                     sync.Mutex{},
		replyCh:                make(map[uint32]chan message.Request),
		logger:                 c.Logger,
		protocolVersion:        c.ProtocolVersion,
		nodeID:                 c.NodeID,
		accessToken:            c.AccessToken,
		intdashExtensionFields: c.IntdashExtensionFields,
		pingInterval:           pingIntervalClient,
		pingTimeout:            pingTimeoutClient,
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
		// runWire の起動は呼び出し元に委ねる。ここで起動すると、呼び出し元が
		// Conn.setE2ECallbacks で onDownstreamCall/onUpstreamCallAck 等を
		// セットする前に readReliableLoop がそれらをロックなしで読んでしまい、
		// データレースになる（呼び出し元は setE2ECallbacks 完了後に go runWire()
		// すること。ConnectWithConfig / connLifecycle.reconnect 参照）。
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
	// readUpstreamChunkAckLoopが担っていたクリーンアップを引き継ぐ
	defer func() {
		c.upstreams.mu.RLock()
		for _, ackCh := range c.upstreams.acks {
			close(ackCh)
		}
		c.upstreams.mu.RUnlock()
	}()

	msgCh := make(chan message.Message)
	go func() {
		defer close(msgCh)
		for {
			msg, err := c.transport.ReadMessage()
			if err != nil {
				if !isTransportCloseError(err) {
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
					if !isTransportCloseError(err) {
						c.logger.Errorf(c.ctx, "%+v", err)
					}
				}
			}()
		case message.Request:
			c.mu.Lock()
			replyCh, ok := c.replyCh[m.GetRequestID()]
			if ok {
				delete(c.replyCh, m.GetRequestID())
			}
			c.mu.Unlock()
			if ok {
				replyCh <- m
			}
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
			c.upstreams.mu.RLock()
			ch, ok := c.upstreams.acks[m.StreamIDAlias]
			c.upstreams.mu.RUnlock()
			if ok {
				select {
				case ch <- m:
				default:
				}
			}
		case *message.DownstreamChunk:
			c.downstreams.mu.RLock()
			ch, ok := c.downstreams.dps[m.StreamIDAlias]
			c.downstreams.mu.RUnlock()
			if ok {
				select {
				case ch <- m:
				default:
				}
			}
		case *message.DownstreamChunkAckComplete:
			c.downstreams.mu.RLock()
			ch, ok := c.downstreams.ackCompletes[m.StreamIDAlias]
			c.downstreams.mu.RUnlock()
			if ok {
				select {
				case ch <- m:
				default:
				}
			}
		case *message.DownstreamMetadata:
			c.downstreams.mu.RLock()
			chs, ok := c.downstreams.metadata[m.StreamIDAlias]
			if ok {
				ch, ok := chs[m.SourceNodeID]
				c.downstreams.mu.RUnlock()
				if ok {
					select {
					case ch <- m:
					default:
					}
				}
			} else {
				c.downstreams.mu.RUnlock()
			}
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
				if !isTransportCloseError(err) {
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
			c.downstreams.mu.RLock()
			ch, ok := c.downstreams.dpsUnreliable[m.StreamIDAlias]
			c.downstreams.mu.RUnlock()
			if ok {
				select {
				case ch <- m:
				default:
				}
			}
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

// encodedUpstreamChunkは、EncodeUpstreamChunkで符号化済みのUpstreamChunkです。
// 符号化時点のトランスポートを保持し、SendEncodedUpstreamChunkはそこへ書き出します。
type encodedUpstreamChunk struct {
	tr *transport.MessageTransport
	em *transport.EncodedMessage
}

// EncodeUpstreamChunkは、UpstreamChunkを送信用に符号化します（送信はしません）。
//
// SendEncodedUpstreamChunkと組で使うことで、符号化と書き込みを別々のタイミングで
// 実行できます（書き込み順序を直列化しつつ符号化は並列に行うため）。
func (c *protocolSession) EncodeUpstreamChunk(req *message.UpstreamChunk) (*encodedUpstreamChunk, error) {
	c.upstreams.mu.RLock()
	tr, ok := c.upstreams.messageWriters[req.StreamIDAlias]
	c.upstreams.mu.RUnlock()

	if !ok {
		return nil, errors.New("stream not exist")
	}
	em, err := tr.EncodeMessage(req)
	if err != nil {
		return nil, err
	}
	return &encodedUpstreamChunk{tr: tr, em: em}, nil
}

// SendEncodedUpstreamChunkは、EncodeUpstreamChunkで符号化済みのUpstreamChunkを送信します。
func (c *protocolSession) SendEncodedUpstreamChunk(ctx context.Context, ec *encodedUpstreamChunk) error {
	return ec.tr.WriteEncodedMessage(ec.em)
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
		pingInterval = defaultPingInterval
	}

	if pingTimeout == 0 {
		pingTimeout = defaultPingTimeout
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
// 受け入れ可能なバージョン: v2.0.0 <= version < v5.0.0
func isAcceptableProtocolVersion(version string) bool {
	// semverパッケージは "v" プレフィックスが必要
	v := "v" + version
	if !semver.IsValid(v) {
		return false
	}
	// minAcceptableVersion <= version < maxAcceptableVersion
	return semver.Compare(v, minAcceptableVersion) >= 0 && semver.Compare(v, maxAcceptableVersion) < 0
}
