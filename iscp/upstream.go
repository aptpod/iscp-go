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
	wireConn *protocolSession

	sentMu      sync.Mutex
	sentBuf     map[uint32]DataPointGroups // seqNum → 送信済みDataPointGroups
	keepPayload bool                       // true: Reliable (payload保存), false: Unreliable/Partial (payload除去)
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

	// lastSendDone は、直近に受理された chunk の「送信試行完了」通知チャネル
	// （mu で保護）。
	//
	// シーケンス番号の採番と同一の mu 臨界区域内で新しいチャネルへ付け替え、
	// 各送信 goroutine（sendChunkAndWaitAck）は自分の直前の chunk の送信試行
	// 完了を待ってから下層へ送信する（FIFO チケットチェーン）。これにより
	// 採番順とワイヤ送信順の一致を保証する。チェーンで直列化されるのは
	// 送信試行までで、Ack 待ちは従来どおり chunk ごとに並行する。
	lastSendDone chan struct{}

	// sendCutoff が true のとき、チケットチェーン上でまだ書き込みを開始して
	// いない chunk の送信を打ち切る。Close がチケットの完了待ちをタイムアウトで
	// 諦めた後に、残った chunk が UpstreamCloseRequest を追い越して wire に
	// 乗るのを防ぐために立てる。
	sendCutoff atomic.Bool

	// Resumeトークン
	resumeToken string

	// runWg は run() の完了を待機するための WaitGroup。
	// resume() は run() の errgroup クリーンアップ完了後に呼ばれる必要がある。
	runWg sync.WaitGroup

	// pendingResend は切断時に保存された未ACKデータ。
	// run() 末尾で設定し、次回 run() 冒頭で消費する。
	// 同一 goroutine 内の順次アクセスのため mutex 不要。
	pendingResend map[uint32]DataPointGroups
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
	// この時点までに受理された chunk の送信試行がすべて完了するのを待つ。
	// closeWithError は UpstreamCloseRequest をチケットチェーンを介さず直接
	// 送信するため、この待機がないと、受理済み chunk が CloseRequest より
	// 後からワイヤに乗ってしまう（FinalSequenceNumber を載せた CloseRequest
	// が chunk を追い越す）。チェーンは FIFO なので、現時点の末尾チケットが
	// 閉じれば先行する全 chunk の送信試行は完了している。
	//
	// 待ちは closeTimeout で必ず打ち切る（上の drain 待ちと同じポリシー）。
	// 下層 transport の Write は内部リトライで長時間ブロックし得るため、
	// 無制限に待つと Close(context.Background()) が返らなくなる。打ち切る場合は
	// sendCutoff を立てて、チェーン上でまだ書き込みを開始していない chunk の
	// 送信を止める（送信させると CloseRequest を追い越すため）。既に下層の
	// Write へ入っている chunk までは止められない。
	//
	// なお、この防止の対象は Close がここで末尾チケットを読んだ時点までに
	// 受理された chunk に限られる。並行して WriteDataPoints / WriteChunk が
	// 走っている場合、これ以降にチェーンへ追加された chunk は保証の対象外
	// （その sequence number は FinalSequenceNumber に含まれ得る）。
	u.mu.RLock()
	lastSendDone := u.lastSendDone
	u.mu.RUnlock()
	// closeTimeout の既定は defaultCloseTimeout（10 秒）。
	// WithUpstreamCloseTimeout(0) が明示された場合は WithTimeout が即時満了する
	// ためこの待ちは行われない。これは既存の drain 待ち
	// （waitToSendAllDataPointsAndReceiveAllAck）が 0 を「graceful close を
	// 待たない」と扱うのと同じ解釈に揃えている。
	waitCtx, waitCancel := context.WithTimeout(ctx, u.closeTimeout)
	defer waitCancel()
	select {
	case <-lastSendDone:
	case <-waitCtx.Done():
		// 保険の作動を運用で検知できるよう warn を残す。
		u.logger.Warnf(ctx, "Close: gave up waiting for in-flight chunk sends (%v); cutting off unsent chunks. upstreamID:[%s]", waitCtx.Err(), u.ID)
		u.sendCutoff.Store(true)
	case <-u.ctx.Done():
		u.sendCutoff.Store(true)
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
	drainCtx, cancel := context.WithTimeout(u.ctx, u.closeTimeout)
	defer cancel()
	if err := u.Flush(ctx); err != nil {
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
		// 通知チャネルは条件（remaining / sendBuffer）を観測する前に取得する。
		// 観測後に取得すると、その間に最後の Ack 処理（removeSent と
		// receivedAckCh の close）が完了した場合、close 済みの旧チャネルでは
		// なく新チャネルを待ってしまい、次の Ack が来るまで（もう来なければ
		// drainCtx のタイムアウトまで）眠り続ける取りこぼしになる。
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
	// 切断時に保存された未ACKデータがあれば再送
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
				resultCh := make(chan *message.UpstreamChunkResult)
				u.mu.Lock()
				u.upstreamChunkResultChs[chunk.StreamChunk.SequenceNumber] = resultCh
				u.mu.Unlock()
				// 再送は本 goroutine 内の同期・直列実行なので、チケット
				// チェーンには参加しない（既に閉じたチャネルを渡す）。
				// 再送 goroutine とチェーンの間の相対順序は保証しない
				// （再送 chunk はサーバー側で sequence number により整列される前提）。
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
	// 切断時、QoS=Reliable の未ACKデータを次回 run() の再送用に保存
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

	// groups 内の DataPoint 数を算出
	var dataPointCount int
	for _, g := range groups {
		dataPointCount += len(g.DataPoints)
	}

	u.mu.Lock()

	if err := u.validateState(dataPointCount); err != nil {
		// NOTE: closeWithError calls stateWithoutLock() which requires u.mu to be held
		u.closeWithError(u.ctx, err)
		u.mu.Unlock()
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

	resultCh := make(chan *message.UpstreamChunkResult)
	u.upstreamChunkResultChs[msgChunk.StreamChunk.SequenceNumber] = resultCh
	// 採番と同一の mu 臨界区域内でチケットを付け替えることで送信順序を保証する。
	prevSendDone := u.lastSendDone
	sendDone := make(chan struct{})
	u.lastSendDone = sendDone
	u.mu.Unlock()

	go u.sendChunkAndWaitAck(ctx, msgChunk, resultCh, prevSendDone, sendDone)
	return nil
}

func (u *Upstream) flush(ctx context.Context) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	if len(u.sendBuffer) == 0 {
		return nil
	}

	if err := u.validateState(u.sendBufferDataPointsCount); err != nil {
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

	u.storeSent(msgChunk.StreamChunk.SequenceNumber, chunk.DataPointGroups)

	resultCh := make(chan *message.UpstreamChunkResult)
	u.upstreamChunkResultChs[msgChunk.StreamChunk.SequenceNumber] = resultCh
	// 採番と同一の mu 臨界区域内でチケットを付け替えることで送信順序を保証する
	// （本関数は defer u.mu.Unlock() で臨界区域内）。
	prevSendDone := u.lastSendDone
	sendDone := make(chan struct{})
	u.lastSendDone = sendDone
	go u.sendChunkAndWaitAck(ctx, msgChunk, resultCh, prevSendDone, sendDone)
	return nil
}

// sendChunkAndWaitAck は chunk を下層へ送信し、Ack を待ち受けます。
//
// prevSendDone は直前に採番された chunk の送信試行完了通知で、これを待って
// から送信することで、採番順とワイヤ送信順の一致を保証する（シーケンス番号の
// 採番はロックで直列化されている一方、本関数は chunk ごとの goroutine で並行
// 実行されるため、この待ち合わせがないと起動順と実行順が入れ替わり、採番順と
// 逆の順序で下層 Write が呼ばれることがある）。
//
// 直列化するのは wire への書き込みだけで、符号化はチケット待ちの前に行う
// （符号化まで直列化するとマルチコアでの連続送信スループットが落ちる）。
//
// sendDone は自分の送信試行の完了通知で、送信の成否・中断に関わらず必ず
// close して後続 chunk の送信を解放する。Ack 待ちはチェーンに含めない
// （前の chunk の Ack を待ってから次を送るわけではない）。
func (u *Upstream) sendChunkAndWaitAck(ctx context.Context, msgChunk *message.UpstreamChunk, resultCh <-chan *message.UpstreamChunkResult, prevSendDone <-chan struct{}, sendDone chan<- struct{}) error {
	// sendDone はどの経路でも必ず閉じる（1 本でも閉じ忘れると後続 chunk の送信が
	// 永久にブロックする）。基本は送信試行の直後に明示的に閉じ、early return への
	// 安全網として defer でも閉じる。本関数を抜けるまで単一 goroutine しか触らない
	// ためフラグに同期は不要。
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

	// 符号化はチケット待ちの前（並列実行される部分）。
	encoded, encodeErr := wireConn.EncodeUpstreamChunk(msgChunk)

	// 符号化に失敗した場合でも、チケットを解放するのは prevSendDone を待ってから
	// にする。待たずに解放すると、直前 chunk の書き込み完了前に後続 chunk の
	// 書き込みが始まり得て、実際に wire に乗る chunk 同士の順序が崩れる。
	select {
	case <-prevSendDone:
	case <-u.ctx.Done():
		// キャンセル時はチェーン全体が同様に打ち切られるため、prevSendDone を
		// 待たずに解放してよい（以降の chunk も送信せずに抜ける）。
		return nil
	}

	if encodeErr != nil {
		return fmt.Errorf("failed to encode upstream chunk[seq:%v]: %w", msgChunk.StreamChunk.SequenceNumber, encodeErr)
	}

	// Close がチケット待ちをタイムアウトで打ち切った後は、ここで送信すると
	// CloseRequest を追い越すため、この chunk は wire に乗せない
	// （チケットは defer で解放される）。
	if u.sendCutoff.Load() {
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

	u.removeSent(msgChunk.StreamChunk.SequenceNumber)

	u.receivedAckMu.Lock()
	close(u.receivedAckCh)
	u.receivedAckCh = make(chan struct{})
	u.receivedAckMu.Unlock()
	return nil
}

func (u *Upstream) clearBuffer() {
	clear(u.sendBuffer)
	u.sendBufferPayloadSize = 0
	u.sendBufferDataPointsCount = 0
}

func (u *Upstream) readAckLoop(ctx context.Context) {
	go u.readResultLoop(ctx)

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
			u.resCh <- ack.Results
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

func (u *Upstream) resume(newConn *protocolSession) error {
	// run() の errgroup クリーンアップ完了を待機。
	// readAckLoop の defer 等がチャネルを close するため、
	// resume() でチャネルを再作成する前に完了していなければならない。
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

	retry.Do(func() (end bool) {
		resp, resErr = u.wireConn.SendUpstreamResumeRequest(u.ctx, &message.UpstreamResumeRequest{
			StreamID:    u.ID,
			ResumeToken: resolveResumeToken(newConn, u.resumeToken),
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

	u.mu.Lock()
	u.ackCh = ch
	u.resCh = make(chan []*message.UpstreamChunkResult, 8)
	u.idAlias = resp.AssignedStreamIDAlias
	u.resumeToken = resolveResumeToken(newConn, resp.ResumeToken)
	u.mu.Unlock()

	// QoS Unreliable の場合、sentBuf をクリア
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
