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

// ConnectWire は、newProtocolSession をテスト用にエクスポートします。
//
// production コードでは newProtocolSession 自体は runWire を起動せず、
// 呼び出し元が setE2ECallbacks 完了後に起動する契約になっている
// （ConnectWithConfig / connLifecycle.reconnect 参照）。ConnectWire は
// テスト用の Config に onDownstreamCall/onUpstreamCallAck 相当の書き込みが
// 無い（= レースの余地が無い）ため、newProtocolSession 呼び出し直後に
// runWire を起動してよい。
func ConnectWire(c *protocolSessionConfig) (*protocolSession, error) {
	conn, err := newProtocolSession(c)
	if err != nil {
		return nil, err
	}
	go conn.runWire()
	return conn, nil
}

// WirePipe は、テスト用のMessageTransportペアを作成します。
func WirePipe() (srv *transport.MessageTransport, cli *transport.MessageTransport) {
	return WirePipeWithSize(0, 0)
}

// WirePipeWithSize は、テスト用のMessageTransportペアを最大メッセージサイズ付きで作成します。
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

// ExportCreateMultiTransport は createMultiTransport をテスト用にエクスポートします。
func (c *ConnConfig) ExportCreateMultiTransport() (transport.Transport, error) {
	return c.createMultiTransport()
}
