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

// BlockSendTicketChain は、チケットチェーンの末尾に閉じられないチケットを
// 差し込み、「送信試行が完了しない chunk が in-flight にある」状態を模擬します。
// Close のチケット待ちがタイムアウトするまで解けなくなります。
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

// WaitRunDoneForTest は run() の完了（runWg が 0 になる）で close される
// チャネルを返します。「run() が readResultLoop の終了を待つか」を外部から
// 観測するためのテスト専用フック。
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

// ConnectWire は、newProtocolSession をテスト用にエクスポートします。
var ConnectWire = newProtocolSession

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
