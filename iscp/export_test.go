package iscp

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"

	uuid "github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/aptpod/iscp-go/log"
	"github.com/aptpod/iscp-go/message"
	"github.com/aptpod/iscp-go/transport"
	"github.com/aptpod/iscp-go/wire"
)

var (
	ToUpstreamDataPointGroups = (*DataPointGroups).toUpstreamDataPointGroups
)

type (
	SequenceNumberGenerator         = sequenceNumberGenerator
	ConnState                       = connStatus
	ConnStatus                      = connStatusValue
	StreamStatus                    = streamStatus
	FlushPolicyNone                 = flushPolicyNone
	FlushPolicyIntervalOnly         = flushPolicyIntervalOnly
	FlushPolicyIntervalOrBufferSize = flushPolicyIntervalOrBufferSize
	FlushPolicyBufferSizeOnly       = flushPolicyBufferSizeOnly
	FlushPolicyImmediately          = flushPolicyImmediately
)

var (
	StreamStatusConnected = streamStatusConnected
	StreamStatusResuming  = streamStatusResuming
	StreamStatusDraining  = streamStatusDraining
)

func (u *Upstream) IsReceivedLastSentAck() bool {
	return u.sequence.CurrentValue() == u.maxSequenceNumberInReceivedUpstreamChunkResults
}

// WaitRunDoneForTest は、呼び出し時点で実行中の run() の完了で close される
// チャネルを返します。「run() が読み取りループ（readResultLoop /
// readAliasLoop）の終了を待つか」を外部から観測するためのテスト専用フック。
func (u *Upstream) WaitRunDoneForTest() <-chan struct{} {
	u.runMu.Lock()
	defer u.runMu.Unlock()
	return u.runDoneCh
}

// ConnStateForTest は、Upstream が監視している connState を返します。
// NewUpstreamForTest で作った Upstream は Conn を介さず connState も専用の
// 独立インスタンスを持つため、直接 Reconnecting へスワップしても Conn 側の
// 再接続ロジックに影響を与えない。flushLoop が run() の errgroup メンバとして
// 不在になる窓を確定的に再現するためのテスト専用フック。
func (u *Upstream) ConnStateForTest() *ConnState {
	return u.connState
}

// SetStreamStateForTest は、Upstream の内部状態を直接指定した状態へ
// スワップし、スワップ前の状態を返します。flushLoop 不在（run() 終了後）の
// 間に streamStatusResuming 以外の状態から Close を呼ぶ窓を、Conn の
// resume 経路を経由せずに確定的に再現するためのテスト専用フック。
func (u *Upstream) SetStreamStateForTest(status StreamStatus) StreamStatus {
	return u.state.Swap(status)
}

// NewUpstreamForTest は、Conn を介さずに Upstream を直接構築します。
// connState / streamState は専用の独立インスタンスなので、テストから
// ConnStateForTest 経由で直接操作しても Conn 側の再接続ロジックに影響しません
// （Conn.OpenUpstream 経由の Upstream は connState を Conn と共有しているため
// 直接操作できません）。wireConn には呼び出し側が用意した *wire.ClientConn を
// そのまま使います。RunForTest で run() を開始するまで内部ループは動きません。
func NewUpstreamForTest(wireConn *wire.ClientConn, id uuid.UUID, idAlias uint32, closeTimeout time.Duration) *Upstream {
	ctx, cancel := context.WithCancel(context.Background())
	conf := defaultUpstreamConfig
	conf.CloseTimeout = &closeTimeout
	return &Upstream{
		ctx:              ctx,
		cancel:           cancel,
		ID:               id,
		dataIDAliases:    map[uint32]*message.DataID{},
		revDataIDAliases: map[message.DataID]uint32{},
		idAlias:          idAlias,
		wireConn:         wireConn,
		sequence:         newSequenceNumberGenerator(0),
		logger:           log.NewNop(),

		ackCh:       make(chan *message.UpstreamChunkAck),
		aliasCh:     make(chan map[uint32]*message.DataID, 8),
		resCh:       make(chan []*message.UpstreamChunkResult, 8),
		dpgCh:       make(chan *DataPointGroup),
		sentBuf:     make(map[uint32]DataPointGroups),
		keepPayload: false,

		closeTimeout: closeTimeout,

		explicitlyFlushCh:       make(chan (<-chan struct{})),
		explicitlyFlushResultCh: make(chan error),
		Config:                  conf,

		eventDispatcher: newEventDispatcher(),

		connState:  newConnState(),
		state:      newStreamState(),
		sendBuffer: map[message.DataID]DataPoints{},

		upstreamChunkResultChs: map[uint32]chan *message.UpstreamChunkResult{},
		receivedAck:            sync.NewCond(&sync.RWMutex{}),
	}
}

// RunForTest は、run() を isResume で goroutine 上で開始します。呼び出し後の
// run() の完了は WaitRunDoneForTest で観測できます。run() が runDoneCh を
// セットするまで待ってから返るため、呼び出し直後に WaitRunDoneForTest を
// 呼んでも nil チャネル（run() 未開始）を掴みません。
func (u *Upstream) RunForTest(isResume bool) {
	go func() {
		_ = u.run(isResume)
	}()
	for {
		u.runMu.Lock()
		ready := u.runDoneCh != nil
		u.runMu.Unlock()
		if ready {
			return
		}
		runtime.Gosched()
	}
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
	customDialFuncsMu.Lock()
	defer customDialFuncsMu.Unlock()
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
