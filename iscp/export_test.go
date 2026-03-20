package iscp

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/aptpod/iscp-go/encoding/protobuf"
	"github.com/aptpod/iscp-go/transport"
)

var (
	ToUpstreamDataPointGroups    = (*DataPointGroups).toUpstreamDataPointGroups
	NewInmemSentStorage          = newInmemSentStorage
	NewInmemSentStorageNoPayload = newInmemSentStorageNoPayload
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
	SentStorage                     = sentStorage
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
	want.sentStorage = got.sentStorage
	want.upstreamRepository = got.upstreamRepository
	want.downstreamRepository = got.downstreamRepository
	assert.Equal(t, want, got)
}

func AssertNotEQConfig(t *testing.T, want, got *ConnConfig) {
	want.sentStorage = got.sentStorage
	want.upstreamRepository = got.upstreamRepository
	want.downstreamRepository = got.downstreamRepository
	assert.NotEqual(t, want, got)
}

// Wire test exports (moved from wire/export_test.go)

func (c *ClientConn) Done() <-chan struct{} {
	return c.ctx.Done()
}

// IsAcceptableProtocolVersion は、isAcceptableProtocolVersion をテスト用にエクスポートします。
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

func SetDefaultPingTimeout(t *testing.T, d time.Duration) {
	org := defaultPingTimeout
	defaultPingTimeout = d

	t.Cleanup(func() {
		defaultPingTimeout = org
	})
}

// ConnectWire は、connectWire をテスト用にエクスポートします。
var ConnectWire = connectWire

// WirePipe は、テスト用のEncodingTransportペアを作成します。
func WirePipe() (srv EncodingTransport, cli EncodingTransport) {
	return WirePipeWithSize(0, 0)
}

// WirePipeWithSize は、テスト用のEncodingTransportペアを最大メッセージサイズ付きで作成します。
func WirePipeWithSize(srvMaxMessageSize, cliMaxMessageSize int64) (srv EncodingTransport, cli EncodingTransport) {
	srvtr, clitr := transport.Pipe()
	srv = transport.NewMessageTransport(&transport.MessageTransportConfig{
		Transport:      srvtr,
		Codec:          protobuf.NewEncoding(),
		MaxMessageSize: srvMaxMessageSize,
	})
	cli = transport.NewMessageTransport(&transport.MessageTransportConfig{
		Transport:      clitr,
		Codec:          protobuf.NewEncoding(),
		MaxMessageSize: cliMaxMessageSize,
	})
	return
}
