package iscp

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/aptpod/iscp-go/v2/encoding/protobuf"
	"github.com/aptpod/iscp-go/v2/transport"
)

var (
	ToUpstreamDataPointGroups = (*DataPointGroups).toUpstreamDataPointGroups
)

type (
	SequenceNumberGenerator         = sequenceNumberGenerator
	ConnState                       = connStatus
	ConnStatus                      = connStatusValue
	FlushPolicyNone                 = flushPolicyNone
	FlushPolicyIntervalOnly         = flushPolicyIntervalOnly
	FlushPolicyIntervalOrBufferSize = flushPolicyIntervalOrBufferSize
	FlushPolicyBufferSizeOnly       = flushPolicyBufferSizeOnly
	FlushPolicyImmediately          = flushPolicyImmediately
	// Wire test type aliases
	ClientConn             = protocolSession
	ClientConnConfig       = protocolSessionConfig
	IntdashExtensionFields = intdashExtensionFields
)

func (u *Upstream) IsReceivedLastSentAck() bool {
	return u.sequence.CurrentValue() == u.maxSequenceNumberInReceivedUpstreamChunkResults
}

var (
	ConnStatusConnected    = connStatusConnected
	ConnStatusReconnecting = connStatusReconnecting
)

func (u *Upstream) SetSendBufferDataPointsCount(t *testing.T, v int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	org := u.sendBufferDataPointsCount
	u.sendBufferDataPointsCount = v
	t.Cleanup(func() {
		u.mu.Lock()
		defer u.mu.Unlock()
		u.sendBufferDataPointsCount = org
	})
}

func (u *Upstream) SetCurrentTotalDataPoints(t *testing.T, v uint64) {
	u.mu.Lock()
	defer u.mu.Unlock()
	org := u.totalDataPoints
	u.totalDataPoints = v
	t.Cleanup(func() {
		u.mu.Lock()
		defer u.mu.Unlock()
		u.totalDataPoints = org
	})
}

func (u *Upstream) SetSequenceNumber(t *testing.T, currentValue uint32) {
	u.mu.Lock()
	defer u.mu.Unlock()
	org := u.sequence
	u.sequence = newSequenceNumberGenerator(currentValue)
	t.Cleanup(func() {
		u.mu.Lock()
		defer u.mu.Unlock()
		u.sequence = org
	})
}

// BlockSendTicketChain appends a ticket that is never closed to the end of the
// ticket chain, simulating a state where a chunk whose send attempt never completes is in flight.
// It stays blocked until Close's ticket wait times out.
func (u *Upstream) BlockSendTicketChain(t *testing.T) {
	u.mu.Lock()
	defer u.mu.Unlock()
	org := u.lastSendDone
	u.lastSendDone = make(chan struct{})
	t.Cleanup(func() {
		u.mu.Lock()
		defer u.mu.Unlock()
		u.lastSendDone = org
	})
}

// WaitRunDoneForTest returns a channel that is closed when run() completes
// (runWg reaches 0). It is a test-only hook to observe from outside
// whether run() waits for readResultLoop to finish.
func (u *Upstream) WaitRunDoneForTest() <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		u.runWg.Wait()
		close(ch)
	}()
	return ch
}

func SetRandomString(t *testing.T, fix string) {
	org := randomString
	randomString = func() string { return fix }
	t.Cleanup(func() {
		randomString = org
	})
}

func RegisterDialer(tr TransportName, f func() transport.Dialer) {
	customDialFuncs[tr] = f
}

func AssertEQConfig(t *testing.T, want, got *ConnConfig) {
	want.upstreamRepository = got.upstreamRepository
	want.downstreamRepository = got.downstreamRepository
	assert.Equal(t, want, got)
}

func AssertNotEQConfig(t *testing.T, want, got *ConnConfig) {
	want.upstreamRepository = got.upstreamRepository
	want.downstreamRepository = got.downstreamRepository
	assert.NotEqual(t, want, got)
}

// Wire test exports (moved from wire/export_test.go)

func (c *protocolSession) Done() <-chan struct{} {
	return c.ctx.Done()
}

// IsAcceptableProtocolVersion exports isAcceptableProtocolVersion for testing.
func IsAcceptableProtocolVersion(version string) bool {
	return isAcceptableProtocolVersion(version)
}

func SetDefaultPingInterval(t *testing.T, d time.Duration) {
	org := defaultPingInterval
	defaultPingInterval = d
	t.Cleanup(func() {
		defaultPingInterval = org
	})
}

func SetConnectHandshakeTimeout(t *testing.T, d time.Duration) {
	org := connectHandshakeTimeout
	connectHandshakeTimeout = d
	t.Cleanup(func() {
		connectHandshakeTimeout = org
	})
}

func SetDefaultPingTimeout(t *testing.T, d time.Duration) {
	org := defaultPingTimeout
	defaultPingTimeout = d

	t.Cleanup(func() {
		defaultPingTimeout = org
	})
}

// ConnectWire exports newProtocolSession for testing.
//
// In production code, newProtocolSession itself does not start runWire;
// the caller is expected to start it after setE2ECallbacks completes
// (see ConnectWithConfig / connLifecycle.reconnect). The test Config used with
// ConnectWire has no writes equivalent to onDownstreamCall/onUpstreamCallAck
// (so there is no room for a race), so runWire can be started right after
// newProtocolSession returns.
func ConnectWire(c *protocolSessionConfig) (*protocolSession, error) {
	conn, err := newProtocolSession(c)
	if err != nil {
		return nil, err
	}
	go conn.runWire()
	return conn, nil
}

// WirePipe creates a pair of MessageTransport for testing.
func WirePipe() (srv *transport.MessageTransport, cli *transport.MessageTransport) {
	return WirePipeWithSize(0, 0)
}

// WirePipeWithSize creates a pair of MessageTransport for testing with maximum message sizes.
func WirePipeWithSize(srvMaxMessageSize, cliMaxMessageSize int64) (srv *transport.MessageTransport, cli *transport.MessageTransport) {
	srvtr, clitr := transport.Pipe()
	srv = transport.NewMessageTransport(&transport.MessageTransportConfig{
		Transport:      srvtr,
		Encoding:       protobuf.NewEncoding(),
		MaxMessageSize: srvMaxMessageSize,
	})
	cli = transport.NewMessageTransport(&transport.MessageTransportConfig{
		Transport:      clitr,
		Encoding:       protobuf.NewEncoding(),
		MaxMessageSize: cliMaxMessageSize,
	})
	return
}

// ExportCreateMultiTransport exports createMultiTransport for testing.
func (c *ConnConfig) ExportCreateMultiTransport() (transport.Transport, error) {
	return c.createMultiTransport()
}
