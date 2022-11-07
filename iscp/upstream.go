package iscp

import (
	"context"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aptpod/iscp-go/errors"
	"github.com/aptpod/iscp-go/internal/retry"

	"github.com/aptpod/iscp-go/log"
	"github.com/aptpod/iscp-go/message"
	"github.com/aptpod/iscp-go/wire"
	uuid "github.com/google/uuid"
	"golang.org/x/sync/errgroup"
)

var (
	defaultFlushInterval   = 100 * time.Millisecond
	defaultCloseTimeout    = 10 * time.Second
	defaultAckInterval     = 100 * time.Millisecond
	defaultFlushBufferSize = 10_000
	defaultExpiryInterval  = time.Second * 10
)

type sequenceNumberGenerator struct {
	Current uint32
}

func newSequenceNumberGenerator(currentValue uint32) *sequenceNumberGenerator {
	return &sequenceNumberGenerator{
		Current: currentValue,
	}
}

func (s *sequenceNumberGenerator) Next() uint32 {
	return atomic.AddUint32(&s.Current, 1)
}

func (s sequenceNumberGenerator) CurrentValue() uint32 {
	return atomic.LoadUint32(&s.Current)
}

// UpstreamStateは、アップストリーム情報です。
type UpstreamState struct {
	DataIDAliases            map[uint32]*message.DataID // データIDとエイリアスのマップ
	TotalDataPoints          uint64                     // 総送信データポイント数
	LastIssuedSequenceNumber uint32                     // 最後に払い出されたシーケンス番号
	DataPointsBuffer         DataPointGroups            // 内部に保存しているデータポイントバッファ
}

// Upstreamは、アップストリームです。
type Upstream struct {
	sync.RWMutex
	ctx    context.Context
	cancel context.CancelFunc

	// properties
	ID         uuid.UUID // ストリームID
	ServerTime time.Time // UpstreamOpenResponseで返却されたサーバー時刻

	// upstream state
	revDataIDAliases                                map[message.DataID]uint32  // データIDとエイリアスのマップ（逆引き用の辞書）
	maxSequenceNumberInReceivedUpstreamChunkResults uint32                     // 受信したUpstreamChunkResult内での最大シーケンス番号
	dataIDAliases                                   map[uint32]*message.DataID // データIDとエイリアスのマップ
	totalDataPoints                                 uint64                     // 総送信データポイント数
	sendBuffer                                      map[message.DataID]DataPoints
	sendBufferPayloadSize                           int
	sendBufferDataPointsCount                       int

	idAlias  uint32
	wireConn *wire.ClientConn

	sent   sentStorage
	logger log.Logger

	ackCh                   <-chan *message.UpstreamChunkAck
	aliasCh                 chan map[uint32]*message.DataID
	resCh                   chan []*message.UpstreamChunkResult
	receivedLastSentAckCh   chan struct{}
	dpgCh                   chan *DataPointGroup
	explicitlyFlushCh       chan (<-chan struct{})
	explicitlyFlushResultCh chan error

	closeTimeout time.Duration
	sequence     *sequenceNumberGenerator

	afterHooker          ReceiveAckHooker
	sendDataPointsHooker SendDataPointsHooker

	eventDispatcher *eventDispatcher

	// Upstreamの設定
	Config UpstreamConfig

	connState *connStatus
	state     *streamState
}

// Stateは、Upstreamが保持している内部の状態を返却します。
func (u *Upstream) State() *UpstreamState {
	u.RLock()
	defer u.RUnlock()
	return u.stateWithoutLock()
}

// Stateは、Upstreamが保持している内部の状態を返却します。
func (u *Upstream) stateWithoutLock() *UpstreamState {
	var res UpstreamState
	res.DataIDAliases = make(map[uint32]*message.DataID, len(u.dataIDAliases))
	// copy DataIDAliases
	for k, v := range u.dataIDAliases {
		res.DataIDAliases[k] = v
	}
	res.LastIssuedSequenceNumber = u.sequence.CurrentValue()
	res.DataPointsBuffer = make(DataPointGroups, 0, len(u.sendBuffer))
	res.TotalDataPoints = u.totalDataPoints
	for k, v := range u.sendBuffer {
		k := k
		// deep copy
		dps := make(DataPoints, len(v))
		copy(dps, v)
		res.DataPointsBuffer = append(res.DataPointsBuffer, &DataPointGroup{
			DataID:     &k,
			DataPoints: dps,
		})
	}
	return &res
}

// Closeは、アップストリームを閉じます。
func (u *Upstream) Close(ctx context.Context, opts ...UpstreamCloseOption) error {
	beforeStatus := u.state.Swap(streamStatusDraining)
	if beforeStatus == streamStatusDraining {
		return errors.New("already draining")
	}
	if beforeStatus != streamStatusResuming {
		if err := u.waitToSendAllDataPointsAndReceiveAllAck(ctx); err != nil {
			u.logger.Warnf(ctx, "Failed to waitSentAllDataPointsAndReceivedAllAck: %+v", err)
		}
	}
	return u.closeWithError(ctx, nil, opts...)
}

func (u *Upstream) closeWithError(ctx context.Context, causeError error, opts ...UpstreamCloseOption) error {
	defer u.cancel()
	if u.isClosed() {
		return nil
	}

	opt := defaultUpstreamCloseOption
	for _, v := range opts {
		v(&opt)
	}

	state := u.stateWithoutLock()
	resp, err := u.wireConn.SendUpstreamCloseRequest(ctx, &message.UpstreamCloseRequest{
		StreamID:            u.ID,
		TotalDataPoints:     state.TotalDataPoints,
		FinalSequenceNumber: state.LastIssuedSequenceNumber,
		ExtensionFields: &message.UpstreamCloseRequestExtensionFields{
			CloseSession: opt.CloseSession,
		},
	})
	if err != nil {
		return err
	}
	if resp.ResultCode != message.ResultCodeSucceeded {
		return errors.FailedMessageError{
			ResultCode:   resp.ResultCode,
			ResultString: resp.ResultString,
			Message:      resp,
		}
	}
	defer func() {
		u.eventDispatcher.addHandler(func() {
			u.Config.ClosedEventHandler.OnUpstreamClosed(&UpstreamClosedEvent{
				Config: u.Config,
				State:  *u.State(),
				Err:    causeError,
			})
		})
	}()
	return nil
}

func (u *Upstream) waitToSendAllDataPointsAndReceiveAllAck(ctx context.Context) error {
	parentCtx, cancel := context.WithCancel(u.ctx)
	defer cancel()
	parentCtx, cancel = context.WithTimeout(parentCtx, u.closeTimeout)
	defer cancel()
	if err := u.Flush(ctx); err != nil {
		return errors.Errorf("failed to flush chunk: %w", err)
	}

	alreadyReceivedLastSentAck := atomic.LoadUint32(&u.maxSequenceNumberInReceivedUpstreamChunkResults) == u.sequence.CurrentValue()
	if alreadyReceivedLastSentAck {
		return nil
	}
	select {
	case <-u.receivedLastSentAckCh:
		return nil
	case <-parentCtx.Done():
		return errors.New("cannot receive final ack because already closed conn")
	case <-ctx.Done():
		return errors.New("receiving ack timed out")
	}
}

func (u *Upstream) isClosed() bool {
	select {
	case <-u.ctx.Done():
		return true
	default:
		return false
	}
}

// WriteDataPointsは、データポイントを内部バッファに書き込みます。
func (u *Upstream) WriteDataPoints(ctx context.Context, dataID *message.DataID, dps ...*message.DataPoint) error {
	if u.isClosed() {
		return errors.ErrStreamClosed
	}
	if u.state.Is(streamStatusDraining) {
		return errors.New("draining")
	}

	select {
	case <-u.ctx.Done():
		return errors.ErrStreamClosed
	case <-ctx.Done():
		return ctx.Err()
	case u.dpgCh <- &DataPointGroup{
		DataID:     dataID,
		DataPoints: dps,
	}:
	}

	return nil
}

func (u *Upstream) run() error {
	ctx, cancel := context.WithCancel(u.ctx)
	defer cancel()
	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		defer u.eventDispatcher.cond.Broadcast()
		defer u.state.cond.Broadcast()
		<-ctx.Done()
		return nil
	})
	eg.Go(func() error {
		u.flushLoop(ctx)
		return nil
	})

	eg.Go(func() error {
		u.readAckLoop(ctx)
		return nil
	})
	eg.Go(func() error {
		u.connState.cond.L.Lock()
		for !u.connState.IsWithoutLock(connStatusReconnecting) {
			select {
			case <-ctx.Done():
				u.connState.cond.L.Unlock()
				return nil
			default:
			}
			u.connState.cond.Wait()
		}
		u.connState.cond.L.Unlock()
		u.state.Swap(streamStatusResuming)
		return errors.New("unexpected disconnected")
	})
	return eg.Wait()
}

func (u *Upstream) flushLoop(ctx context.Context) {
	ticker, stop := u.Config.FlushPolicy.Ticker()
	defer stop()

	flushFunc := func() (isContinue bool) {
		err := u.flush()
		if err == nil {
			return true
		}
		if !errors.Is(err, errors.ErrConnectionClosed) {
			u.logger.Errorf(u.ctx, "failed to flush: %+v", err)
			return true
		}
		u.state.cond.L.Lock()
		u.logger.Warnf(u.ctx, "failed to flush: %+v", err)
		for !u.state.IsWithoutLock(streamStatusResuming) {
			select {
			case <-ctx.Done():
				u.state.cond.L.Unlock()
				return false
			default:
			}
			u.state.cond.Wait()
		}
		u.state.cond.L.Unlock()
		return true
	}

	for {
		select {
		case <-ctx.Done():
			if err := u.flush(); err != nil {
				u.logger.Errorf(u.ctx, "failed to flush: %+v", err)
			}
			u.sendBufferDataPointsCount = 0
			return
		case remoteDone := <-u.explicitlyFlushCh:
			err := u.flush()
			select {
			case u.explicitlyFlushResultCh <- err:
			case <-remoteDone:
			case <-ctx.Done():
			}
			continue
		case <-ticker:
			if !flushFunc() {
				return
			}
		case dpg := <-u.dpgCh:
			u.Lock()
			if _, ok := u.sendBuffer[*dpg.DataID]; ok {
				u.sendBuffer[*dpg.DataID] = append(u.sendBuffer[*dpg.DataID], dpg.DataPoints...)
			} else {
				u.sendBuffer[*dpg.DataID] = make([]*message.DataPoint, 0, len(dpg.DataPoints))
				u.sendBuffer[*dpg.DataID] = append(u.sendBuffer[*dpg.DataID], dpg.DataPoints...)
			}
			u.sendBufferPayloadSize += dpg.PayloadSize()
			u.sendBufferDataPointsCount += len(dpg.DataPoints)
			if !u.Config.FlushPolicy.IsFlush(uint32(u.sendBufferPayloadSize)) {
				u.Unlock()
				continue
			}
			u.Unlock()
			if !flushFunc() {
				return
			}
		}
	}
}

// Flushは、データポイントの内部バッファをUpstreamChunkとしてサーバーへ送信します。
func (u *Upstream) Flush(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if u.isClosed() {
		return errors.ErrStreamClosed
	}
	select {
	case u.explicitlyFlushCh <- ctx.Done():
	case <-u.ctx.Done():
		return errors.ErrStreamClosed
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-u.ctx.Done():
		return errors.ErrStreamClosed
	case err := <-u.explicitlyFlushResultCh:
		return err
	}
}

func (u *Upstream) flush() error {
	u.Lock()
	defer u.Unlock()

	if len(u.sendBuffer) == 0 {
		return nil
	}

	before := u.totalDataPoints
	newVal := atomic.AddUint64(&u.totalDataPoints, uint64(u.sendBufferDataPointsCount))
	if before > newVal {
		atomic.StoreUint64(&u.totalDataPoints, before)
		err := fmt.Errorf("total datapoints exceeded max value")
		u.closeWithError(u.ctx, err)
		return err
	}

	if u.sequence.CurrentValue() == math.MaxUint32 {
		atomic.StoreUint64(&u.totalDataPoints, before)
		err := fmt.Errorf("sequence number exceeded max")
		u.closeWithError(u.ctx, err)
		return err
	}

	seq := u.sequence.Next()

	dpgs := make(DataPointGroups, 0, len(u.sendBuffer))
	for id, dps := range u.sendBuffer {
		id := id
		dpgs = append(dpgs, &DataPointGroup{
			DataID:     &id,
			DataPoints: dps,
		})
	}
	if u.sendDataPointsHooker != nil {
		u.eventDispatcher.addHandler(func() {
			u.sendDataPointsHooker.HookBefore(u.ID, UpstreamChunk{SequenceNumber: seq, DataPointGroups: dpgs})
		})
	}

	u.sendBuffer = map[message.DataID]DataPoints{}
	u.sendBufferPayloadSize = 0
	u.sendBufferDataPointsCount = 0

	if err := u.sent.Store(u.ctx, u.ID, seq, dpgs); err != nil {
		return err
	}

	dpg, ids := dpgs.toUpstreamDataPointGroups(u.revDataIDAliases)

	return u.wireConn.SendUpstreamChunk(u.ctx, &message.UpstreamChunk{
		StreamIDAlias: u.idAlias,
		DataIDs:       ids,
		StreamChunk: &message.StreamChunk{
			SequenceNumber:  seq,
			DataPointGroups: dpg,
		},
	})
}

func (u *Upstream) ackOrDone(ctx context.Context) <-chan *message.UpstreamChunkAck {
	ch := make(chan *message.UpstreamChunkAck)
	go func() {
		defer close(ch)
		for {
			select {
			case m, ok := <-u.ackCh:
				if !ok {
					return
				}
				ch <- m
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch
}

func (u *Upstream) readAckLoop(ctx context.Context) {
	go u.readResultLoop()
	go u.readAliasLoop()

	defer close(u.aliasCh)
	defer close(u.resCh)

	for ack := range u.ackOrDone(ctx) {
		u.aliasCh <- ack.DataIDAliases
		u.resCh <- ack.Results
	}
}

func (u *Upstream) readResultLoop() {
	for v := range u.resCh {
		for _, vv := range v {
			if err := u.processResult(vv); err != nil {
				u.logger.Errorf(u.ctx, "failed to processResult: %+v", err)
				continue
			}
		}
	}
}

func (u *Upstream) readAliasLoop() {
	for v := range u.aliasCh {
		u.processDataIDAliases(v)
	}
}

func (u *Upstream) processDataIDAliases(aliases map[uint32]*message.DataID) {
	u.Lock()
	defer u.Unlock()

	for a, id := range aliases {
		if _, ok := u.revDataIDAliases[*id]; ok {
			continue
		}
		u.revDataIDAliases[*id] = a
		u.dataIDAliases[a] = id
	}
}

func (u *Upstream) processResult(result *message.UpstreamChunkResult) error {
	ctx := u.ctx
	_, err := u.sent.Remove(ctx, u.ID, result.SequenceNumber)
	if err != nil {
		return errors.Errorf("invalid sequence number: %w", err)
	}

	if u.afterHooker != nil {
		u.eventDispatcher.addHandler(func() {
			u.afterHooker.HookAfter(u.ID, UpstreamChunkAck{SequenceNumber: result.SequenceNumber, DataPointsAck: DataPointsAck{
				ResultCode:   result.ResultCode,
				ResultString: result.ResultString,
			}})
		})
	}

	if atomic.LoadUint32(&u.maxSequenceNumberInReceivedUpstreamChunkResults) < result.SequenceNumber {
		atomic.StoreUint32(&u.maxSequenceNumberInReceivedUpstreamChunkResults, result.SequenceNumber)
	}

	select {
	case <-u.ctx.Done():
		return nil
	default:
	}

	if u.state.Is(streamStatusDraining) {
		remaining, err := u.sent.Remaining(ctx, u.ID)
		if err != nil {
			u.logger.Warnf(ctx, "failed to remaining: %+v", err)
		} else {
			if result.SequenceNumber == u.sequence.CurrentValue() && remaining == 0 {
				close(u.receivedLastSentAckCh)
			}
		}
	}
	return nil
}

func (u *Upstream) resume(newConn *wire.ClientConn) error {
	if u.isClosed() {
		return fmt.Errorf("already closed upstream")
	}
	if !u.state.Is(streamStatusResuming) {
		return fmt.Errorf("invalid state want[%v] but[%v]", streamStatusResuming, u.state.Current())
	}
	u.wireConn = newConn

	var resp *message.UpstreamResumeResponse
	var resErr error

	retry.Do(func() (end bool) {
		resp, resErr = u.wireConn.SendUpstreamResumeRequest(u.ctx, &message.UpstreamResumeRequest{
			StreamID: u.ID,
		}, u.Config.QoS)
		if resErr != nil {
			return true
		}
		if resp.ResultCode == message.ResultCodeSucceeded {
			resErr = nil
			return true
		}
		resErr = &errors.FailedMessageError{
			ResultCode:   resp.ResultCode,
			ResultString: resp.ResultString,
			Message:      resp,
		}
		return resp.ResultCode != message.ResultCodeResumeRequestConflict
	})
	if resErr != nil {
		u.closeWithError(u.ctx, resErr)
		return errors.Errorf("failed send upstream resume request: %w", resErr)
	}

	ch, err := u.wireConn.SubscribeUpstreamChunkAck(u.ctx, resp.AssignedStreamIDAlias)
	if err != nil {
		return errors.Errorf("failed to SubscribeUpstreamChunkAck: %w", err)
	}
	u.ackCh = ch
	u.aliasCh = make(chan map[uint32]*message.DataID, 8)
	u.resCh = make(chan []*message.UpstreamChunkResult, 8)
	u.idAlias = resp.AssignedStreamIDAlias

	u.eventDispatcher.addHandler(func() {
		u.Config.ResumedEventHandler.OnUpstreamResumed(&UpstreamResumedEvent{
			ID:     u.ID,
			Config: u.Config,
			State:  *u.State(),
		})
	})
	u.state.Swap(streamStatusConnected)
	return nil
}
