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
	mu     sync.RWMutex
	ctx    context.Context
	cancel context.CancelFunc

	ID         uuid.UUID      // ストリームID
	ServerTime time.Time      // UpstreamOpenResponseで返却されたサーバー時刻
	Config     UpstreamConfig // Upstreamの設定

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

	ackCh   <-chan *message.UpstreamChunkAck
	aliasCh chan map[uint32]*message.DataID
	resCh   chan []*message.UpstreamChunkResult

	dpgCh                   chan *DataPointGroup
	explicitlyFlushCh       chan (<-chan struct{})
	explicitlyFlushResultCh chan error

	closeTimeout time.Duration
	sequence     *sequenceNumberGenerator

	afterHooker          ReceiveAckHooker
	sendDataPointsHooker SendDataPointsHooker

	eventDispatcher *eventDispatcher

	connState *connStatus
	state     *streamState

	upstreamChunkResultChs map[uint32]chan *message.UpstreamChunkResult
	receivedAck            *sync.Cond
}

// Stateは、Upstreamが保持している内部の状態を返却します。
func (u *Upstream) State() *UpstreamState {
	u.mu.RLock()
	defer u.mu.RUnlock()
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
			ResultCode:      resp.ResultCode,
			ResultString:    resp.ResultString,
			ReceivedMessage: resp,
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

	u.receivedAck.L.Lock()
	var err error
	var remaining map[uint32]DataPointGroups
LOOP:
	for {
		select {
		case <-parentCtx.Done():
			err = errors.New("cannot receive final ack because already closed conn")
			break LOOP
		case <-ctx.Done():
			err = errors.New("receiving ack timed out")
			break LOOP
		default:
		}
		remaining, err = u.sent.List(ctx, u.ID)
		if err != nil {
			break
		}

		u.mu.Lock()
		lengthSendBuffer := len(u.sendBuffer)
		u.mu.Unlock()
		if lengthSendBuffer == 0 && len(remaining) == 0 {
			break
		}
		u.receivedAck.Wait()
	}
	u.receivedAck.L.Unlock()
	return err
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

func (u *Upstream) run(isResume bool) error {
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
	if isResume && u.Config.QoS == message.QoSReliable {
		eg.Go(func() error {
			m, err := u.sent.List(ctx, u.ID)
			if err != nil {
				return nil
			}
			for seqNum, dpgs := range m {
				u.mu.Lock()
				dpg, ids := dpgs.toUpstreamDataPointGroups(u.revDataIDAliases)
				u.mu.Unlock()
				chunk := &message.UpstreamChunk{
					StreamIDAlias: u.idAlias,
					DataIDs:       ids,
					StreamChunk: &message.StreamChunk{
						SequenceNumber:  seqNum,
						DataPointGroups: dpg,
					},
				}
				resultCh := make(chan *message.UpstreamChunkResult)
				u.mu.Lock()
				u.upstreamChunkResultChs[chunk.StreamChunk.SequenceNumber] = resultCh
				u.mu.Unlock()
				u.sendChunkAndWaitAck(ctx, chunk, resultCh)
				u.logger.Debugf(u.ctx, "Resent data point groups[seqNum=%v, count=%v].", seqNum, len(dpg))
			}
			return nil
		})
	} else if isResume {
		u.sent.Clear(u.ctx, u.ID)
	}
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

	for {
		select {
		case <-ctx.Done():
			if err := u.flush(ctx); err != nil {
				u.logger.Errorf(u.ctx, "failed to flush: %+v", err)
			}
			return
		case remoteDone := <-u.explicitlyFlushCh:
			select {
			case u.explicitlyFlushResultCh <- u.flush(ctx):
			case <-remoteDone:
			case <-ctx.Done():
			}
			continue
		case <-ticker:
			u.flush(ctx)
		case dpg := <-u.dpgCh:
			u.mu.Lock()
			if _, ok := u.sendBuffer[*dpg.DataID]; ok {
				u.sendBuffer[*dpg.DataID] = append(u.sendBuffer[*dpg.DataID], dpg.DataPoints...)
			} else {
				u.sendBuffer[*dpg.DataID] = make([]*message.DataPoint, 0, len(dpg.DataPoints))
				u.sendBuffer[*dpg.DataID] = append(u.sendBuffer[*dpg.DataID], dpg.DataPoints...)
			}
			u.sendBufferPayloadSize += dpg.payloadSize()
			u.sendBufferDataPointsCount += len(dpg.DataPoints)
			if !u.Config.FlushPolicy.IsFlush(uint32(u.sendBufferPayloadSize)) {
				u.mu.Unlock()
				continue
			}
			u.mu.Unlock()
			u.flush(ctx)
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

func (u *Upstream) validateState() error {
	before := atomic.LoadUint64(&u.totalDataPoints)
	newVal := before + uint64(u.sendBufferDataPointsCount)
	if before > newVal {
		return fmt.Errorf("total datapoints exceeded max value")
	}

	if u.sequence.CurrentValue() == math.MaxUint32 {
		return fmt.Errorf("sequence number exceeded max")
	}
	return nil
}

var UpstreamDefaultAckTimeout = time.Second

func (u *Upstream) toUpstreamChunk() (*message.UpstreamChunk, *UpstreamChunk) {
	dpgs := make(DataPointGroups, 0, len(u.sendBuffer))
	for id, dps := range u.sendBuffer {
		id := id
		dpgs = append(dpgs, &DataPointGroup{
			DataID:     &id,
			DataPoints: dps,
		})
	}

	dpg, ids := dpgs.toUpstreamDataPointGroups(u.revDataIDAliases)
	chunk := &message.UpstreamChunk{
		StreamIDAlias: u.idAlias,
		DataIDs:       ids,
		StreamChunk: &message.StreamChunk{
			SequenceNumber:  u.sequence.Next(),
			DataPointGroups: dpg,
		},
	}

	return chunk, &UpstreamChunk{
		SequenceNumber:  chunk.StreamChunk.SequenceNumber,
		DataPointGroups: dpgs,
	}
}

func (u *Upstream) flush(ctx context.Context) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	if len(u.sendBuffer) == 0 {
		return nil
	}

	if err := u.validateState(); err != nil {
		u.closeWithError(u.ctx, err)
		return err
	}

	atomic.AddUint64(&u.totalDataPoints, uint64(u.sendBufferDataPointsCount))

	msgChunk, chunk := u.toUpstreamChunk()
	u.clearBuffer()

	if u.sendDataPointsHooker != nil {
		u.eventDispatcher.addHandler(func() {
			u.sendDataPointsHooker.HookBefore(u.ID, *chunk)
		})
	}

	if err := u.sent.Store(u.ctx, u.ID, msgChunk.StreamChunk.SequenceNumber, chunk.DataPointGroups); err != nil {
		return err
	}

	resultCh := make(chan *message.UpstreamChunkResult)
	u.upstreamChunkResultChs[msgChunk.StreamChunk.SequenceNumber] = resultCh
	go u.sendChunkAndWaitAck(ctx, msgChunk, resultCh)
	return nil
}

func (u *Upstream) sendChunkAndWaitAck(ctx context.Context, msgChunk *message.UpstreamChunk, resultCh chan *message.UpstreamChunkResult) {
	u.mu.Lock()
	err := u.wireConn.SendUpstreamChunk(u.ctx, msgChunk)
	u.mu.Unlock()
	if err != nil {
		u.logger.Warnf(u.ctx, "failed to send upstream chunk[seq:%v]: %+v", msgChunk.StreamChunk.SequenceNumber, err)
		return
	}

	_, ok := <-u.withAckTimeoutCh(ctx, resultCh)
	if !ok {
		return
	}
	if atomic.LoadUint32(&u.maxSequenceNumberInReceivedUpstreamChunkResults) < msgChunk.StreamChunk.SequenceNumber {
		atomic.StoreUint32(&u.maxSequenceNumberInReceivedUpstreamChunkResults, msgChunk.StreamChunk.SequenceNumber)
	}

	u.receivedAck.Broadcast()
	_, err = u.sent.Remove(u.ctx, u.ID, msgChunk.StreamChunk.SequenceNumber)
	if err != nil {
		u.logger.Errorf(u.ctx, "invalid sequence number: %+v", err)
	}
}

func (u *Upstream) withAckTimeoutCh(ctx context.Context, inCh <-chan *message.UpstreamChunkResult) <-chan *message.UpstreamChunkResult {
	resCh := make(chan *message.UpstreamChunkResult)
	go func() {
		defer close(resCh)
		ctx, cancel := ctx, context.CancelFunc(func() {})
		if u.Config.AckTimeout != 0 {
			ctx, cancel = context.WithTimeout(ctx, u.Config.AckTimeout)
		}
		defer cancel()
		select {
		case <-ctx.Done():
		case <-u.ctx.Done():
		case val, ok := <-inCh:
			if !ok {
				return
			}
			select {
			case <-ctx.Done():
			case <-u.ctx.Done():
			case resCh <- val:
			}
		}
	}()
	return resCh
}

func (u *Upstream) clearBuffer() {
	u.sendBuffer = map[message.DataID]DataPoints{}
	u.sendBufferPayloadSize = 0
	u.sendBufferDataPointsCount = 0
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
				select {
				case ch <- m:
				case <-ctx.Done():
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch
}

func (u *Upstream) readAckLoop(ctx context.Context) {
	go u.readResultLoop(ctx)
	go u.readAliasLoop(ctx)

	defer close(u.aliasCh)
	defer close(u.resCh)

	for ack := range u.ackOrDone(ctx) {
		u.aliasCh <- ack.DataIDAliases
		u.resCh <- ack.Results
	}
}

func (u *Upstream) readResultLoop(ctx context.Context) {
	defer func() {
		u.mu.Lock()
		defer u.mu.Unlock()
		for _, v := range u.upstreamChunkResultChs {
			close(v)
		}
		u.upstreamChunkResultChs = make(map[uint32]chan *message.UpstreamChunkResult)
	}()
	for v := range u.resCh {
		for _, vv := range v {
			vv := vv

			if u.afterHooker != nil {
				u.eventDispatcher.addHandler(func() {
					u.afterHooker.HookAfter(u.ID, UpstreamChunkResult{
						SequenceNumber: vv.SequenceNumber,
						ResultCode:     vv.ResultCode,
						ResultString:   vv.ResultString,
					})
				})
			}

			if err := u.processResult(ctx, vv); err != nil {
				u.logger.Errorf(u.ctx, "failed to processResult: %+v", err)
				continue
			}
		}
	}
}

func (u *Upstream) readAliasLoop(ctx context.Context) {
	for v := range u.aliasCh {
		u.processDataIDAliases(v)
	}
}

func (u *Upstream) processDataIDAliases(aliases map[uint32]*message.DataID) {
	u.mu.Lock()
	defer u.mu.Unlock()

	for a, id := range aliases {
		if _, ok := u.revDataIDAliases[*id]; ok {
			continue
		}
		u.revDataIDAliases[*id] = a
		u.dataIDAliases[a] = id
	}
}

func (u *Upstream) processResult(ctx context.Context, result *message.UpstreamChunkResult) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	ch, ok := u.upstreamChunkResultChs[result.SequenceNumber]
	if !ok {
		return nil
	}
	select {
	case <-ctx.Done():
	case <-u.ctx.Done():
	case ch <- result:
	}
	delete(u.upstreamChunkResultChs, result.SequenceNumber)
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
			ResultCode:      resp.ResultCode,
			ResultString:    resp.ResultString,
			ReceivedMessage: resp,
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
