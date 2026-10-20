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

	// connectHandshakeTimeout is the upper limit on the handshake from sending
	// ConnectRequest to receiving ConnectResponse. It prevents dial from blocking
	// indefinitely when the server accepts the connection but does not respond
	// (it is a var so that tests can shorten it).
	//
	// Rationale for 30 seconds (do not shorten it without grounds):
	//   - Leave ample margin for the handshake round trip on high-latency
	//     networks such as satellite links (RTT up to about 1.5s) plus server
	//     processing time. Since no setting is exposed to relax it, choose a
	//     large enough value so that legitimate connections do not expire
	//   - The transport dial is a separate phase before this limit, and the two
	//     do not compete. The configured limit on the dial side is DialTimeout
	//     on the websocket path (10s by default, honored by both coder and
	//     gorilla), and for quic / webtransport it is the caller's ctx and
	//     quic-go's HandshakeIdleTimeout (5s by default, aborting at twice that).
	//     For websocket this is shorter than gorilla's former default of 45s, so
	//     the total time until one attempt is determined to have failed
	//     (dial + handshake) is not needlessly extended
	//   - Liveness monitoring after the handshake completes is the job of
	//     PingInterval / PingTimeout (10s / 1s by default), and this value is
	//     not involved
	connectHandshakeTimeout = 30 * time.Second

	// ErrUnsupportedProtocolVersion は、サーバーが返したプロトコルバージョンがサポートされていない場合のエラーです。
	ErrUnsupportedProtocolVersion = errors.New("unsupported protocol version")

	// ErrUnauthorized は、認証されていないときに返されます。
	ErrUnauthorized = errors.Errorf("unauthorized : %w", ErrInvalidConnectRequest)

	// ErrInvalidConnectRequest は、ConnectRequestが不正の場合に返されます。
	ErrInvalidConnectRequest = errors.New("invalid connect request")

	// minAcceptableVersion is the minimum acceptable protocol version (inclusive).
	minAcceptableVersion = "v2.0.0"
	// maxAcceptableVersion is the maximum acceptable protocol version (exclusive).
	maxAcceptableVersion = "v5.0.0"
	// resumeTokenMinVersion is the minimum protocol version that supports ResumeToken.
	resumeTokenMinVersion = "v3.0.0"
	// pingPongMinVersion is the minimum protocol version that does not use iSCP Ping/Pong.
	// At or above this version, the transport-level heartbeat is used.
	pingPongMinVersion = "v4.0.0"
)

// isTransportCloseError determines whether the error is caused by a normal close of the transport.
func isTransportCloseError(err error) bool {
	return errors.Is(err, transport.ErrAlreadyClosed) ||
		errors.Is(err, transport.EOF) ||
		errors.Is(err, errors.ErrConnectionClose) ||
		errors.Is(err, net.ErrClosed)
}

// protocolSession is the client-side connection.
type protocolSession struct {
	transport           *transport.MessageTransport
	unreliableTransport *transport.MessageTransport

	idGenerator IDGenerator

	ctx    context.Context
	cancel context.CancelFunc

	// onDownstreamCall is a callback invoked when a DownstreamCall message is received.
	onDownstreamCall func(*message.DownstreamCall)
	// onUpstreamCallAck is a callback invoked when an UpstreamCallAck message is received.
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

// intdashExtensionFields are the extension fields for the intdash API.
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

// protocolSessionConfig is the configuration of the client connection.
type protocolSessionConfig struct {
	// Context is the context for interrupting the handshake (ConnectRequest to
	// ConnectResponse). If nil, the handshake is not interrupted (only the
	// connectHandshakeTimeout watchdog is the upper limit). It has no bearing on
	// the lifecycle after the session is established. This struct is a parameter
	// object built for each dial.
	Context context.Context

	// Transport is the transport.
	Transport *transport.MessageTransport

	// UnreliableTransport is the unreliable transport. If nil, the reliable transport is used when the QoS is Unreliable.
	UnreliableTransport *transport.MessageTransport

	// Logger is the logger.
	Logger log.Logger

	// ProtocolVersion is the supported protocol version.
	ProtocolVersion string

	// NodeID is the ID of the node that opens the client connection.
	NodeID string

	// AccessToken is the access token used when connecting with iSCP.
	AccessToken string

	// IntdashExtensionFields are the extension fields for the intdash API.
	IntdashExtensionFields *intdashExtensionFields

	// PingInterval is the interval at which iSCP Ping messages are sent.
	PingInterval time.Duration

	// PingTimeout is the timeout from sending an iSCP Ping until the Pong is returned.
	//
	// On timeout, the iSCP connection is disconnected.
	PingTimeout time.Duration

	// OnDownstreamCall is a callback invoked when a DownstreamCall message is received.
	OnDownstreamCall func(*message.DownstreamCall)

	// OnUpstreamCallAck is a callback invoked when an UpstreamCallAck message is received.
	OnUpstreamCallAck func(*message.UpstreamCallAck)
}

// newProtocolSession performs the iSCP connection and returns a protocolSession.
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

	// Set a deadline on the handshake (from sending ConnectRequest to receiving
	// ConnectResponse). The transport has no way to set a read deadline, so when
	// the deadline is exceeded, the transport is closed to unblock WriteMessage /
	// ReadMessage. This protects dial (newProtocolSession) from blocking
	// indefinitely when the server accepts the connection but does not respond.
	//
	// If the deadline expires at the same moment the handshake completes, there
	// is in theory a race that closes the just-established session, but in that
	// case it recovers via the caller's reconnect path (it is treated the same
	// as a disconnect after the session is established).
	//
	// Note that this protection depends on transport.Close() returning. Whether
	// Close is bounded depends on the underlying transport, but
	// reconnect.Transport interrupts an in-progress dial with its own ctx on
	// Close, so it is bounded (only when a legacy Dialer is plugged in can it
	// wait up to the dialer's internal timeout; see the comment on close() in
	// conn.go).
	watchdog := time.AfterFunc(connectHandshakeTimeout, func() {
		conn.logger.Warnf(ctx, "Connect handshake timed out after %v. Closing transport.", connectHandshakeTimeout)
		conn.transport.Close()
	})
	// Make it possible to interrupt the handshake with the caller's ctx (the
	// lifecycle ctx on reconnect) as well. This releases the transport held by
	// the reconnect dial of a closed Conn waiting on the handshake earlier than
	// the watchdog deadline. Because of the deferred stop, the established
	// session is not closed even if the caller's ctx dies after the handshake
	// completes (after this function returns).
	if c.Context != nil {
		stop := context.AfterFunc(c.Context, func() {
			conn.logger.Warnf(ctx, "Connect handshake aborted by caller context. Closing transport.")
			conn.transport.Close()
		})
		defer stop()
	}
	msg, err := conn.waitForConnected(pingIntervalServer, pingTimeoutServer)
	watchdog.Stop()
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
		// Starting runWire is left to the caller. If it were started here,
		// readReliableLoop would read onDownstreamCall/onUpstreamCallAck etc. without
		// locks before the caller sets them with Conn.setE2ECallbacks, causing a data
		// race (the caller must run go runWire() after setE2ECallbacks completes; see
		// ConnectWithConfig / connLifecycle.reconnect).
		return conn, nil
	default:
		return nil, errors.FailedMessageError{
			ResultCode:      msg.ResultCode,
			ResultString:    msg.ResultString,
			ReceivedMessage: msg,
		}
	}
}

// Closed returns a channel for checking whether the protocolSession is closed.
//
// If the protocolSession is closed, the channel is closed.
func (c *protocolSession) Closed() <-chan struct{} {
	return c.ctx.Done()
}

// ProtocolVersion returns the protocol version returned by the server.
func (c *protocolSession) ProtocolVersion() string {
	return c.protocolVersion
}

// SupportsResumeToken returns whether the current connection supports ResumeToken.
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
	// Take over the cleanup that readUpstreamChunkAckLoop used to do
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
			// Send Pong inline (avoid blocking in a separate goroutine)
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
			// Handle Disconnect inline
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

// Close closes the client connection.
func (c *protocolSession) Close() error {
	c.cancel()
	return c.transport.Close()
}

// UnderlyingTransport returns the transport used internally.
func (c *protocolSession) UnderlyingTransport() transport.ReadWriter {
	return c.transport.UnderlyingTransport()
}

// SendDisconnect sends a Disconnect message.
func (c *protocolSession) SendDisconnect(ctx context.Context, msg *message.Disconnect) error {
	return c.transport.WriteMessage(msg)
}

// SendUpstreamMetadata sends an UpstreamMetadata.
func (c *protocolSession) SendUpstreamMetadata(ctx context.Context, msg *message.UpstreamMetadata) (*message.UpstreamMetadataAck, error) {
	msg.RequestID = message.RequestID(c.idGenerator.Next())
	res, err := c.sendRequest(ctx, msg)
	if err != nil {
		return nil, err
	}
	return res.(*message.UpstreamMetadataAck), nil
}

// SubscribeUpstreamChunkAck waits for UpstreamChunkAck.
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

// SendUpstreamOpenRequest sends an UpstreamOpenRequest.
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

// SendUpstreamResumeRequest sends an UpstreamResumeRequest.
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

// SendUpstreamChunk sends an UpstreamChunk.
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

// encodedUpstreamChunk is an UpstreamChunk already encoded by EncodeUpstreamChunk.
// It holds the transport at the time of encoding, and SendEncodedUpstreamChunk writes to it.
type encodedUpstreamChunk struct {
	tr *transport.MessageTransport
	em *transport.EncodedMessage
}

// EncodeUpstreamChunk encodes an UpstreamChunk for sending (it does not send).
//
// Used together with SendEncodedUpstreamChunk, encoding and writing can be
// done at different times (to serialize the write order while encoding in parallel).
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

// SendEncodedUpstreamChunk sends an UpstreamChunk already encoded by EncodeUpstreamChunk.
func (c *protocolSession) SendEncodedUpstreamChunk(ctx context.Context, ec *encodedUpstreamChunk) error {
	return ec.tr.WriteEncodedMessage(ec.em)
}

// SendUpstreamCloseRequest sends an UpstreamCloseRequest.
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

// SubscribeDownstreamChunk waits for DownstreamChunk of the specified stream ID alias and QoS.
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

// SubscribeDownstreamChunkAckComplete waits for DownstreamChunkAckComplete of the specified stream ID alias.
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

// SubscribeDownstreamMeta waits for DownstreamMetadata of the specified stream ID alias and node ID.
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

// SendDownstreamResumeRequest sends a DownstreamResumeRequest.
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

// SendDownstreamOpenRequest sends a DownstreamOpenRequest.
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

// SendDownstreamCloseRequest sends a DownstreamCloseRequest.
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

// SendDownstreamDataPointsAck sends a DownstreamMetadataAck.
func (c *protocolSession) SendDownstreamDataPointsAck(ctx context.Context, ack *message.DownstreamChunkAck) error {
	return c.transport.WriteMessage(ack)
}

// SendDownstreamMetadataAck sends a DownstreamMetadataAck.
func (c *protocolSession) SendDownstreamMetadataAck(ctx context.Context, ack *message.DownstreamMetadataAck) error {
	return c.transport.WriteMessage(ack)
}

// SendUpstreamCall sends an UpstreamCall.
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

// needsPingPong returns whether iSCP-level Ping/Pong keepalive is needed.
// It returns true when ProtocolVersion < 4.0.0.
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

// isAcceptableProtocolVersion determines whether the protocol version returned by the server is acceptable.
// Acceptable versions: v2.0.0 <= version < v5.0.0
func isAcceptableProtocolVersion(version string) bool {
	// The semver package requires the "v" prefix
	v := "v" + version
	if !semver.IsValid(v) {
		return false
	}
	// minAcceptableVersion <= version < maxAcceptableVersion
	return semver.Compare(v, minAcceptableVersion) >= 0 && semver.Compare(v, maxAcceptableVersion) < 0
}
