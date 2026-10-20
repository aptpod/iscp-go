package iscp

import (
	"context"
	"fmt"
	"maps"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aptpod/iscp-go/v2/errors"
	"github.com/aptpod/iscp-go/v2/internal/retry"

	uuid "github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"github.com/aptpod/iscp-go/v2/log"
	"github.com/aptpod/iscp-go/v2/message"
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

	// closeRequested is a guard that limits the body of closeWithError (sending
	// CloseRequest, firing the Closed event, u.cancel) to the first call only.
	// See the comment on closeWithError for details.
	closeRequested atomic.Bool

	ID         uuid.UUID      // ストリームID
	ServerTime time.Time      // UpstreamOpenResponseで返却されたサーバー時刻
	Config     UpstreamConfig // Upstreamの設定

	// upstream state
	revDataIDAliases                                map[message.DataID]uint32  // Map of data IDs to aliases (dictionary for reverse lookup)
	maxSequenceNumberInReceivedUpstreamChunkResults uint32                     // Maximum sequence number among the received UpstreamChunkResults
	dataIDAliases                                   map[uint32]*message.DataID // Map of data IDs to aliases
	totalDataPoints                                 uint64                     // Total number of data points sent
	sendBuffer                                      map[message.DataID]DataPoints
	sendBufferPayloadSize                           int
	sendBufferDataPointsCount                       int

	idAlias  uint32
	wireConn *protocolSession

	sentMu      sync.Mutex
	sentBuf     map[uint32]DataPointGroups // seqNum -> sent DataPointGroups
	keepPayload bool                       // true: Reliable (keep payload), false: Unreliable/Partial (drop payload)
	logger      log.Logger

	ackCh <-chan *message.UpstreamChunkAck
	resCh chan []*message.UpstreamChunkResult

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
	receivedAckMu          sync.Mutex
	receivedAckCh          chan struct{}

	// lastSendDone is the "send attempt completed" notification channel of the
	// most recently accepted chunk (protected by mu).
	//
	// It is replaced with a new channel within the same mu critical section as
	// sequence number assignment, and each sending goroutine
	// (sendChunkAndWaitAck) waits for the send attempt of the chunk immediately
	// before it to complete before sending to the lower layer (a FIFO ticket
	// chain). This guarantees that the assignment order matches the wire send
	// order. Only up to the send attempt is serialized by the chain; waiting for
	// Ack remains concurrent per chunk, as before.
	lastSendDone chan struct{}

	// When sendCutoff is true, sending of chunks on the ticket chain that have
	// not yet started writing is aborted. It is set to prevent remaining chunks
	// from getting onto the wire ahead of UpstreamCloseRequest after Close gives
	// up waiting for tickets to complete on timeout.
	sendCutoff atomic.Bool

	// Resume token
	resumeToken string

	// runWg is a WaitGroup for waiting for run() to complete.
	// resume() must be called after the errgroup cleanup of run() has completed.
	runWg sync.WaitGroup

	// pendingResend is the unACKed data saved on disconnect.
	// It is set at the end of run() and consumed at the start of the next run().
	// No mutex is needed because it is accessed sequentially within the same goroutine.
	pendingResend map[uint32]DataPointGroups
}

// Stateは、Upstreamが保持している内部の状態を返却します。
func (u *Upstream) State() *UpstreamState {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.stateWithoutLock()
}

// stateWithoutLock returns the internal state held by the Upstream.
// It iterates over dataIDAliases / sendBuffer (both maps rewritten under
// u.mu), so the caller must hold u.mu (RLock is enough because it only
// reads).
func (u *Upstream) stateWithoutLock() *UpstreamState {
	var res UpstreamState
	res.DataIDAliases = make(map[uint32]*message.DataID, len(u.dataIDAliases))
	// copy DataIDAliases
	for k, v := range u.dataIDAliases {
		res.DataIDAliases[k] = v
	}
	res.LastIssuedSequenceNumber = u.sequence.CurrentValue()
	res.DataPointsBuffer = make(DataPointGroups, 0, len(u.sendBuffer))
	// The writers (flush / WriteChunk) use atomic.AddUint64, so align the read
	// to be atomic as well.
	res.TotalDataPoints = atomic.LoadUint64(&u.totalDataPoints)
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
//
// WithUpstreamCloseTimeout で設定する closeTimeout は、drain（バッファ済み
// データポイントの送信と ack の受信）の待ち上限であり、Close 全体の上限では
// ありません。その後の CloseRequest の送信はこの上限に含まれません。
//
// 閉じる経路（Close の並行呼び出し・内部のエラー経路）が並行に重なった
// 場合、tear-down（CloseRequest の送信と Closed イベントの発火）を行うのは
// 最初の 1 経路だけです。それ以外の呼び出しは tear-down の完了を待たずに
// nil を返します。
func (u *Upstream) Close(ctx context.Context, opts ...UpstreamCloseOption) error {
	beforeStatus := u.state.Swap(streamStatusDraining)
	if beforeStatus == streamStatusDraining {
		return errors.New("already draining")
	}
	// Treat closeTimeout as the upper limit for the whole of Close. Close has
	// three waits (Flush, waiting for the ack drain, and waiting for the ticket
	// chain), and if each took closeTimeout independently, Close could wait up
	// to a multiple of it. So a single deadline is fixed at the entry, and all
	// the waits share the remaining time.
	closeDeadline := time.Now().Add(u.closeTimeout)
	if beforeStatus != streamStatusResuming {
		if err := u.waitToSendAllDataPointsAndReceiveAllAck(ctx, closeDeadline); err != nil {
			u.logger.Warnf(ctx, "Failed to waitSentAllDataPointsAndReceivedAllAck: %+v", err)
		}
	}
	// Wait for the send attempts of all chunks accepted up to this point to
	// complete. closeWithError sends UpstreamCloseRequest directly without going
	// through the ticket chain, so without this wait, an accepted chunk could get
	// onto the wire after the CloseRequest (the CloseRequest carrying
	// FinalSequenceNumber would be overtaken by the chunk). The chain is FIFO, so
	// once the current tail ticket is closed, the send attempts of all preceding
	// chunks have completed.
	//
	// The wait is always aborted by closeTimeout (the same policy as the drain
	// wait above). Write on the lower-layer transport can block for a long time
	// with internal retries, so waiting without a limit would keep
	// Close(context.Background()) from returning. When it is aborted, set
	// sendCutoff to stop sending chunks on the chain that have not yet passed the
	// sendCutoff check (sending them would overtake the CloseRequest). A chunk
	// that has already passed the check (at most one, since the chain is
	// serial) cannot be stopped.
	//
	// Note that this prevention covers only the chunks accepted by the time Close
	// reads the tail ticket here. If WriteDataPoints / WriteChunk are running
	// concurrently, chunks added to the chain after this point are not covered
	// (their sequence numbers may be included in FinalSequenceNumber).
	u.mu.RLock()
	lastSendDone := u.lastSendDone
	u.mu.RUnlock()
	// The deadline is closeDeadline (10 seconds by default), fixed at the entry
	// of Close. If the drain wait above has used up the time, this wait is
	// aborted immediately. When WithUpstreamCloseTimeout(0) is explicitly
	// specified, the deadline expires immediately, so this wait is not performed.
	// This aligns with how the existing drain wait
	// (waitToSendAllDataPointsAndReceiveAllAck) treats 0 as "do not wait for
	// graceful close".
	waitCtx, waitCancel := context.WithDeadline(ctx, closeDeadline)
	defer waitCancel()
	select {
	case <-lastSendDone:
	case <-waitCtx.Done():
		// Leave a warn so that the activation of this safety net can be detected in
		// operation. However, it does not fire on the immediate expiry when
		// WithUpstreamCloseTimeout(0) is explicit (behavior as configured) or on
		// cancellation of the caller's ctx (context.Canceled).
		if u.closeTimeout > 0 && errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
			u.logger.Warnf(ctx, "Close: gave up waiting for in-flight chunk sends (%v); cutting off unsent chunks. upstreamID:[%s]", waitCtx.Err(), u.ID)
		}
		u.sendCutoff.Store(true)
	case <-u.ctx.Done():
		u.sendCutoff.Store(true)
	}
	return u.closeWithError(ctx, nil, opts...)
}

func (u *Upstream) closeWithError(ctx context.Context, causeError error, opts ...UpstreamCloseOption) error {
	if u.isClosed() {
		return nil
	}
	// Guard against multiple calls. Four paths reach this function (Close,
	// resume failure, and validateState failure in flush and WriteChunk), and
	// since flush / WriteChunk call it after releasing u.mu, they can overlap
	// concurrently. Only the winner sends CloseRequest and fires the Closed
	// event, and the loser returns nil immediately (the same treatment as the
	// former early return on isClosed). u.cancel() is limited to the winner's
	// defer because if a loser canceled first, it would abort the RPC the winner
	// is sending (the internal path uses a ctx derived from u.ctx).
	if !u.closeRequested.CompareAndSwap(false, true) {
		return nil
	}
	defer u.cancel()

	// Only this winner's defer can call u.cancel() (u.ctx has no parent, and
	// there is only this one caller of cancel). So protecting the winner's own
	// waits with u.ctx would create a wait that is released only when it itself
	// returns (self-reference). Internal error paths must go through
	// closeWithErrorBounded and pass a ctx bounded by closeTimeout.

	opt := defaultUpstreamCloseOption
	for _, v := range opts {
		v(&opt)
	}

	// Read the state under u.mu. dataIDAliases / sendBuffer are maps rewritten
	// under u.mu, so iterating without the lock can die immediately with
	// fatal error: concurrent map iteration and map write (the Close / resume
	// failure paths used to reach here without the lock). wireConn is also
	// replaced by resume under u.mu, so take it here (same as in
	// sendChunkAndWaitAck). The send is placed outside the lock because a network
	// round trip while holding u.mu would stall the receive paths
	// (processDataIDAliases / processResult etc.) in a chain reaction.
	u.mu.RLock()
	state := u.stateWithoutLock()
	wireConn := u.wireConn
	u.mu.RUnlock()

	resp, err := wireConn.SendUpstreamCloseRequest(ctx, &message.UpstreamCloseRequest{
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

// closeWithErrorBounded is a wrapper for calling closeWithError from internal
// error paths (resume failure, validateState failure). If u.ctx were passed
// as is, nobody could release its own wait when the send blocks (see the
// comment on the guard in closeWithError), so it is bounded by closeTimeout.
// Paths that enter from the user's Close(ctx) use the caller's ctx as is.
func (u *Upstream) closeWithErrorBounded(causeError error) error {
	ctx, cancel := context.WithTimeout(u.ctx, u.closeTimeout)
	defer cancel()
	return u.closeWithError(ctx, causeError)
}

// waitToSendAllDataPointsAndReceiveAllAck flushes the send buffer and waits
// until the Ack of every sent chunk is received. The deadline is the deadline
// for the whole of Close (closeTimeout worth), fixed at the entry of Close.
// The parent is u.ctx so that the wait is aborted when the Conn is closed
// (the caller's ctx is watched separately in the select).
func (u *Upstream) waitToSendAllDataPointsAndReceiveAllAck(ctx context.Context, deadline time.Time) error {
	drainCtx, cancel := context.WithDeadline(u.ctx, deadline)
	defer cancel()
	// Apply the deadline for the whole of Close to Flush as well (if it is not
	// passed, only Flush falls outside the deadline, breaking the promise that
	// "closeTimeout is the upper limit for the whole of Close"). The parent of
	// drainCtx is u.ctx, so passing it as is would make cancellation of the
	// caller's ctx ineffective. Create a separate deadline ctx whose parent is
	// the caller's ctx.
	flushCtx, flushCancel := context.WithDeadline(ctx, deadline)
	defer flushCancel()
	if err := u.Flush(flushCtx); err != nil {
		return errors.Errorf("failed to flush chunk: %w", err)
	}

	alreadyReceivedLastSentAck := atomic.LoadUint32(&u.maxSequenceNumberInReceivedUpstreamChunkResults) == u.sequence.CurrentValue()
	if alreadyReceivedLastSentAck {
		return nil
	}

	for {
		select {
		case <-drainCtx.Done():
			return errors.New("cannot receive final ack because already closed conn")
		case <-ctx.Done():
			return errors.New("receiving ack timed out")
		default:
		}
		// Get the notification channel before observing the conditions (remaining /
		// sendBuffer). If it is obtained after observing, and the last Ack processing
		// (removeSent and the close of receivedAckCh) completes in between, we would
		// wait on the new channel instead of the already-closed old one, and sleep
		// until the next Ack arrives (or until the drainCtx timeout if none comes),
		// a missed wakeup.
		u.receivedAckMu.Lock()
		ch := u.receivedAckCh
		u.receivedAckMu.Unlock()

		remaining := u.listSent()

		u.mu.Lock()
		lengthSendBuffer := len(u.sendBuffer)
		u.mu.Unlock()
		if lengthSendBuffer == 0 && len(remaining) == 0 {
			return nil
		}

		select {
		case <-ch:
			continue
		case <-drainCtx.Done():
			return errors.New("cannot receive final ack because already closed conn")
		case <-ctx.Done():
			return errors.New("receiving ack timed out")
		}
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

// Deprecated: NewWriter と Writer.Write を使用してください。
//
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

// NewWriter は、指定DataIDへのWriterを作成します。
func (u *Upstream) NewWriter(dataID *message.DataID) *UpstreamWriter {
	return &UpstreamWriter{
		dataID:   dataID,
		upstream: u,
	}
}

func (u *Upstream) run() error {
	u.runWg.Add(1)
	defer u.runWg.Done()
	ctx, cancel := context.WithCancel(u.ctx)
	defer cancel()
	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		u.flushLoop(ctx)
		return nil
	})

	eg.Go(func() error {
		u.readAckLoop(ctx)
		return nil
	})

	// Make readResultLoop a member of the errgroup too, so that run() waits for
	// it to finish (up to the deferred channel close and map recreation). The
	// termination chain holds in this order: ctx cancel -> readAckLoop returns
	// (closes resCh in defer) -> readResultLoop leaves the range and runs its
	// defer.
	eg.Go(func() error {
		u.readResultLoop(ctx)
		return nil
	})
	// Resend unACKed data saved on disconnect, if any
	pendingResend := u.pendingResend
	u.pendingResend = nil
	if len(pendingResend) > 0 {
		eg.Go(func() error {
			for seqNum, dpgs := range pendingResend {
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
				// cap 1 is required (do not revert to unbuffered). See the comment on processResult for the rationale.
				resultCh := make(chan *message.UpstreamChunkResult, 1)
				u.mu.Lock()
				u.upstreamChunkResultChs[chunk.StreamChunk.SequenceNumber] = resultCh
				u.mu.Unlock()
				// The resend runs synchronously and serially within this goroutine, so it
				// does not participate in the ticket chain (pass an already-closed channel).
				// The relative order between the resend goroutine and the chain is not
				// guaranteed (resent chunks are assumed to be ordered by sequence number on
				// the server side).
				prevSendDone := make(chan struct{})
				close(prevSendDone)
				if err := u.sendChunkAndWaitAck(ctx, chunk, resultCh, prevSendDone, make(chan struct{})); err != nil {
					u.logger.Warnf(u.ctx, "%+v", err)
					u.mu.Lock()
					delete(u.upstreamChunkResultChs, chunk.StreamChunk.SequenceNumber)
					u.mu.Unlock()
				}
				u.logger.Debugf(u.ctx, "Resent data point groups[seqNum=%v, count=%v].", seqNum, len(dpg))
			}
			return nil
		})
	}
	eg.Go(func() error {
		return waitForReconnecting(ctx, u.connState, u.state)
	})
	err := eg.Wait()
	// On disconnect, save unACKed data of QoS=Reliable for resending in the next run()
	if err != nil && u.Config.QoS == message.QoSReliable {
		if m := u.listSent(); len(m) > 0 {
			u.pendingResend = m
		}
	}
	return err
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

func (u *Upstream) validateState(dataPointCount int) error {
	before := atomic.LoadUint64(&u.totalDataPoints)
	newVal := before + uint64(dataPointCount)
	if before > newVal {
		return fmt.Errorf("total datapoints exceeded max value")
	}

	if u.sequence.CurrentValue() == math.MaxUint32 {
		return fmt.Errorf("sequence number exceeded max")
	}
	return nil
}

func (u *Upstream) toUpstreamChunk() (*message.UpstreamChunk, *UpstreamChunk) {
	groups := make([]*DataPointGroup, 0, len(u.sendBuffer))
	for id, dps := range u.sendBuffer {
		id := id
		groups = append(groups, &DataPointGroup{
			DataID:     &id,
			DataPoints: dps,
		})
	}
	return u.toUpstreamChunkFromGroups(groups)
}

func (u *Upstream) toUpstreamChunkFromGroups(groups []*DataPointGroup) (*message.UpstreamChunk, *UpstreamChunk) {
	dpgs := DataPointGroups(groups)
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

// WriteChunk は、複数のDataPointGroupを1つのChunkとして即座に送信します。
// シーケンス番号は内部で自動的に割り当てられます。
func (u *Upstream) WriteChunk(ctx context.Context, groups ...*DataPointGroup) error {
	if len(groups) == 0 {
		return nil
	}
	if u.isClosed() {
		return errors.ErrStreamClosed
	}
	if u.state.Is(streamStatusDraining) {
		return errors.New("draining")
	}

	// Count the DataPoints in groups
	var dataPointCount int
	for _, g := range groups {
		dataPointCount += len(g.DataPoints)
	}

	u.mu.Lock()

	if err := u.validateState(dataPointCount); err != nil {
		// closeWithError takes u.mu.RLock internally. Calling it while holding the
		// non-reentrant u.mu would deadlock on itself, so release it first.
		u.mu.Unlock()
		u.closeWithErrorBounded(err)
		return err
	}

	atomic.AddUint64(&u.totalDataPoints, uint64(dataPointCount))
	msgChunk, chunk := u.toUpstreamChunkFromGroups(groups)

	if u.sendDataPointsHooker != nil {
		u.eventDispatcher.addHandler(func() {
			u.sendDataPointsHooker.HookBefore(u.ID, *chunk)
		})
	}

	u.storeSent(msgChunk.StreamChunk.SequenceNumber, chunk.DataPointGroups)

	// cap 1 is required (do not revert to unbuffered). See the comment on processResult for the rationale.
	resultCh := make(chan *message.UpstreamChunkResult, 1)
	u.upstreamChunkResultChs[msgChunk.StreamChunk.SequenceNumber] = resultCh
	// Swapping the ticket within the same mu critical section as the assignment guarantees the send order.
	prevSendDone := u.lastSendDone
	sendDone := make(chan struct{})
	u.lastSendDone = sendDone
	u.mu.Unlock()

	go u.sendChunkAndWaitAck(ctx, msgChunk, resultCh, prevSendDone, sendDone)
	return nil
}

func (u *Upstream) flush(ctx context.Context) error {
	u.mu.Lock()

	if len(u.sendBuffer) == 0 {
		u.mu.Unlock()
		return nil
	}

	if err := u.validateState(u.sendBufferDataPointsCount); err != nil {
		// closeWithError takes u.mu.RLock internally. Calling it while holding the
		// non-reentrant u.mu would deadlock on itself, so release it first.
		u.mu.Unlock()
		u.closeWithErrorBounded(err)
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

	u.storeSent(msgChunk.StreamChunk.SequenceNumber, chunk.DataPointGroups)

	// cap 1 is required (do not revert to unbuffered). See the comment on processResult for the rationale.
	resultCh := make(chan *message.UpstreamChunkResult, 1)
	u.upstreamChunkResultChs[msgChunk.StreamChunk.SequenceNumber] = resultCh
	// Swapping the ticket within the same mu critical section as the assignment
	// guarantees the send order (the critical section of this function extends
	// to the u.mu.Unlock() right after, not to a defer. If the swap were moved
	// after Unlock, u.lastSendDone would become a data race and the send order
	// guarantee would also be lost).
	prevSendDone := u.lastSendDone
	sendDone := make(chan struct{})
	u.lastSendDone = sendDone
	u.mu.Unlock()

	go u.sendChunkAndWaitAck(ctx, msgChunk, resultCh, prevSendDone, sendDone)
	return nil
}

// sendChunkAndWaitAck sends a chunk to the lower layer and waits for the Ack.
//
// prevSendDone is the send-attempt completion notification of the chunk
// numbered immediately before. Waiting for it before sending guarantees that
// the assignment order matches the wire send order (sequence numbers are
// assigned serialized by a lock, but this function runs concurrently in a
// goroutine per chunk, so without this synchronization the start order and
// execution order could be swapped and the lower-layer Write could be called
// in the reverse of the assignment order).
//
// Only the write to the wire is serialized; encoding is done before waiting
// for the ticket (serializing the encoding as well would lower the
// continuous send throughput on multi-core).
//
// sendDone is the completion notification of its own send attempt, and it is
// always closed to release the sending of subsequent chunks, regardless of
// whether the send succeeds or is aborted. Waiting for Ack is not included in
// the chain (the next chunk is not sent only after the previous chunk's Ack).
func (u *Upstream) sendChunkAndWaitAck(ctx context.Context, msgChunk *message.UpstreamChunk, resultCh <-chan *message.UpstreamChunkResult, prevSendDone <-chan struct{}, sendDone chan<- struct{}) error {
	// sendDone must be closed on every path (if even one is left unclosed, the
	// sending of subsequent chunks blocks forever). Basically it is closed
	// explicitly right after the send attempt, and also in a defer as a safety
	// net for early returns. Only a single goroutine touches it until this
	// function exits, so the flag needs no synchronization.
	sendDoneClosed := false
	closeSendDone := func() {
		if !sendDoneClosed {
			sendDoneClosed = true
			close(sendDone)
		}
	}
	defer closeSendDone()

	u.mu.RLock()
	wireConn := u.wireConn
	u.mu.RUnlock()

	// Encoding is before waiting for the ticket (the part that runs in parallel).
	encoded, encodeErr := wireConn.EncodeUpstreamChunk(msgChunk)

	// Even when encoding fails, release the ticket only after waiting for
	// prevSendDone. If it is released without waiting, the write of a subsequent
	// chunk could start before the previous chunk's write completes, breaking the
	// order among the chunks that actually get onto the wire.
	select {
	case <-prevSendDone:
	case <-u.ctx.Done():
		// On cancellation, the ordering guarantee is abandoned. This abort is
		// best-effort: a chunk that has already entered write cannot be stopped, and
		// the select of a subsequent chunk released early may pick the prevSendDone
		// side and proceed to write.
		return nil
	}

	if encodeErr != nil {
		return fmt.Errorf("failed to encode upstream chunk[seq:%v]: %w", msgChunk.StreamChunk.SequenceNumber, encodeErr)
	}

	// After Close has given up waiting for tickets, sending here would overtake
	// the CloseRequest, so this chunk is not put on the wire (the ticket is
	// released in a defer). The sequence number of the dropped chunk is included
	// in FinalSequenceNumber, so leave a warn so that it can be detected in
	// operation.
	//
	// The abort happens not only when closeTimeout expires but also on
	// cancellation of the caller's ctx or of u.ctx via Conn.Close(), so the
	// wording does not depend on timeout. upstreamID is included to identify
	// which stream has the gap in FinalSequenceNumber in an environment with
	// multiple upstreams.
	if u.sendCutoff.Load() {
		u.logger.Warnf(u.ctx, "Close cut off unsent chunk; seq:%d upstreamID:[%s]", msgChunk.StreamChunk.SequenceNumber, u.ID)
		return nil
	}

	err := wireConn.SendEncodedUpstreamChunk(u.ctx, encoded)
	closeSendDone()
	if err != nil {
		return fmt.Errorf("failed to send upstream chunk[seq:%v]: %w", msgChunk.StreamChunk.SequenceNumber, err)
	}

	var result *message.UpstreamChunkResult
	if u.Config.AckTimeout > 0 {
		timer := time.NewTimer(u.Config.AckTimeout)
		defer timer.Stop()
		select {
		case <-timer.C:
			u.logger.Warnf(u.ctx, "ack timeout for seq %d", msgChunk.StreamChunk.SequenceNumber)
			return nil
		case <-u.ctx.Done():
			return nil
		case r := <-resultCh:
			result = r
		}
	} else {
		select {
		case <-u.ctx.Done():
			return nil
		case r := <-resultCh:
			result = r
		}
	}

	if result != nil {
		for {
			old := atomic.LoadUint32(&u.maxSequenceNumberInReceivedUpstreamChunkResults)
			if msgChunk.StreamChunk.SequenceNumber <= old {
				break
			}
			if atomic.CompareAndSwapUint32(&u.maxSequenceNumberInReceivedUpstreamChunkResults, old, msgChunk.StreamChunk.SequenceNumber) {
				break
			}
		}
	}

	// Removal from sentBuf and waking up the drain are not done here. Removal is
	// driven by "the arrival of Ack" (processResult), and does not depend on
	// whether the waiter is alive.
	return nil
}

func (u *Upstream) clearBuffer() {
	clear(u.sendBuffer)
	u.sendBufferPayloadSize = 0
	u.sendBufferDataPointsCount = 0
}

// readAckLoop relays acks to resCh. readResultLoop is started in the errgroup
// of run(), not here. If it were started with go so that run() does not wait
// for it to finish, the readResultLoop of the previous session generation
// would stay alive while runWg.Wait() in resume() passes straight through,
// and the late-running defer (closing all upstreamChunkResultChs and
// recreating the map) would close the next generation's live channels,
// treating chunks whose ack has not been received as acked (data loss from
// the QoS Reliable resend set).
func (u *Upstream) readAckLoop(ctx context.Context) {
	defer func() {
		u.mu.Lock()
		close(u.resCh)
		u.mu.Unlock()
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case ack, ok := <-u.ackCh:
			if !ok {
				return
			}
			u.processDataIDAliases(ack.DataIDAliases)
			// Do not make this a bare send. If the reader (readResultLoop) is stalled by
			// contention on u.mu or the like, resCh (cap 8) can stay full and never
			// clear, and a send that does not watch ctx blocks there forever. readAckLoop
			// is a member of the errgroup of run(), so if it does not return here,
			// eg.Wait() -> run() -> runWg.Done() becomes unreachable, resume()'s
			// runWg.Wait() blocks forever, and reconnection of the whole Conn stops.
			select {
			case u.resCh <- ack.Results:
			case <-ctx.Done():
				// The cause of termination is ctx cancellation (disconnect / Close), and the
				// loss of this ack is treated the same as the unprocessed ones left in resCh.
				// No error is returned (a normal exit, the same as the existing ctx.Done
				// branch).
				return
			}
		}
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

	// When an Ack arrives, remove the entry from the "unacked data" (sentBuf) and
	// wake up the Close drain wait. If the waiter (sendChunkAndWaitAck) were
	// responsible for removal, then for a chunk whose waiter left earlier due to
	// a send error or ack timeout, nobody would reflect its Ack, the entry would
	// become unsatisfiable, and every later Close would use up closeDeadline (the
	// cutoff warning would fire even with no in-flight chunk). The sentBuf
	// entries are themselves the QoS Reliable resend set, so they must not be
	// removed on any trigger other than the arrival of Ack, while once an Ack
	// arrives they should be removed regardless of whether the waiter is alive.
	//
	// Place this before the !ok check below. Even when the send error path of
	// pendingResend has already removed the map entry, the entry must be removed
	// from sentBuf once the Ack arrives. The wakeup (close and recreation of
	// receivedAckCh) is done as a pair. With removeSent alone, listSent() would
	// become empty but the drain would not be woken, sleeping until the deadline.
	u.removeSent(result.SequenceNumber)
	u.receivedAckMu.Lock()
	close(u.receivedAckCh)
	u.receivedAckCh = make(chan struct{})
	u.receivedAckMu.Unlock()

	ch, ok := u.upstreamChunkResultChs[result.SequenceNumber]
	if !ok {
		return nil
	}
	// ch has cap 1, so this send always completes immediately. This is because
	// each channel is sent to at most once (it is deleted after the send, and
	// second and later Acks for the same seq are ignored by the !ok check above).
	//
	// Do not revert it to unbuffered: on each of the send error / ack timeout /
	// sendCutoff / u.ctx cancellation paths, sendChunkAndWaitAck returns without
	// waiting for the Ack while leaving the upstreamChunkResultChs entry in
	// place. If the Ack for that seq arrives afterwards, a send to a channel with
	// no reader would block while holding u.mu (writer), and flush /
	// WriteDataPoints / Close / readResultLoop (all subsequent Ack processing)
	// would stall in a chain. "The send failed but the server returns an Ack"
	// really occurs with partially-sent errors in the multi transport and with
	// write timeouts (errors after being put in the send buffer).
	select {
	case <-ctx.Done():
	case <-u.ctx.Done():
	case ch <- result:
	}
	delete(u.upstreamChunkResultChs, result.SequenceNumber)
	return nil
}

func (u *Upstream) resume(newConn *protocolSession) error {
	// Wait for the errgroup cleanup of run() to complete.
	// The defer in readAckLoop and others close channels, so it must have
	// completed before the channels are recreated in resume().
	//
	// Note: this Wait has no ctx exit. Its boundedness depends solely on all
	// members of the run() errgroup terminating on ctx cancellation. If a wait
	// that does not watch ctx (a bare channel send/receive, I/O while holding a
	// lock, etc.) is added to a member, this will no longer return and
	// reconnection of the whole Conn will stall. Also note that goroutines
	// started outside the errgroup are not covered by this contract (run() does
	// not wait for them to finish, which causes defers to execute across
	// generations).
	u.runWg.Wait()
	if u.isClosed() {
		return fmt.Errorf("already closed upstream")
	}
	if !u.state.Is(streamStatusResuming) {
		return fmt.Errorf("invalid state want[%v] but[%v]", streamStatusResuming, u.state.Current())
	}
	u.mu.Lock()
	u.wireConn = newConn
	u.mu.Unlock()

	var resp *message.UpstreamResumeResponse
	var resErr error
	resumed := false

	// Pass u.ctx so that cancellation takes effect immediately even during the sleep between retries.
	// With retry.Do (fixed to Background), the call could not return after cancellation
	// until the in-progress sleep (up to about 7.5 seconds with exponential backoff) expired.
	// The request itself has always watched u.ctx, so if already canceled
	// it reaches end immediately with an error.
	retry.DoWithContext(u.ctx, func() (end bool) {
		resp, resErr = u.wireConn.SendUpstreamResumeRequest(u.ctx, &message.UpstreamResumeRequest{
			StreamID:    u.ID,
			ResumeToken: resolveResumeToken(newConn, u.resumeToken),
		}, u.Config.QoS)
		if resErr != nil {
			return true
		}
		if resp.ResultCode == message.ResultCodeSucceeded {
			resErr = nil
			resumed = true
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
		u.closeWithErrorBounded(resErr)
		return errors.Errorf("failed send upstream resume request: %w", resErr)
	}
	if !resumed {
		resErr = u.ctx.Err()
		if resErr == nil {
			resErr = errors.New("upstream resume did not complete")
		}
		u.closeWithErrorBounded(resErr)
		return errors.Errorf("failed send upstream resume request: %w", resErr)
	}

	ch, err := u.wireConn.SubscribeUpstreamChunkAck(u.ctx, resp.AssignedStreamIDAlias)
	if err != nil {
		return errors.Errorf("failed to SubscribeUpstreamChunkAck: %w", err)
	}

	u.mu.Lock()
	u.ackCh = ch
	u.resCh = make(chan []*message.UpstreamChunkResult, 8)
	u.idAlias = resp.AssignedStreamIDAlias
	u.resumeToken = resolveResumeToken(newConn, resp.ResumeToken)
	u.mu.Unlock()

	// For QoS Unreliable, clear sentBuf
	if u.Config.QoS != message.QoSReliable {
		u.clearSent()
	}

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

func (u *Upstream) storeSent(seqNum uint32, dpgs DataPointGroups) {
	u.sentMu.Lock()
	defer u.sentMu.Unlock()
	if u.keepPayload {
		u.sentBuf[seqNum] = dpgs
	} else {
		u.sentBuf[seqNum] = dpgs.withoutPayload()
	}
}

func (u *Upstream) removeSent(seqNum uint32) {
	u.sentMu.Lock()
	defer u.sentMu.Unlock()
	delete(u.sentBuf, seqNum)
}

func (u *Upstream) listSent() map[uint32]DataPointGroups {
	u.sentMu.Lock()
	defer u.sentMu.Unlock()
	if len(u.sentBuf) == 0 {
		return nil
	}
	result := make(map[uint32]DataPointGroups, len(u.sentBuf))
	maps.Copy(result, u.sentBuf)
	return result
}

func (u *Upstream) clearSent() {
	u.sentMu.Lock()
	defer u.sentMu.Unlock()
	clear(u.sentBuf)
}
