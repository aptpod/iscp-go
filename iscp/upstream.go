package iscp

import (
	"context"
	"fmt"
	"maps"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aptpod/iscp-go/errors"
	"github.com/aptpod/iscp-go/internal/retry"

	uuid "github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"github.com/aptpod/iscp-go/log"
	"github.com/aptpod/iscp-go/message"
	"github.com/aptpod/iscp-go/wire"
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

	// closeRequested は closeWithError の実体（CloseRequest 送信・Closed
	// イベント発火・u.cancel）を最初の 1 回に限定するガード。
	// 詳細は closeWithError のコメント参照。
	closeRequested atomic.Bool

	// runDoneCh はテスト専用。run() の呼び出しごとに新しいチャネルに
	// 差し替え、その回の run() の完了で close する。「run() が読み取り
	// ループの終了まで待ってから返るか」を外部から観測するためだけに使う
	// （WaitRunDoneForTest 参照）。run() は同一 goroutine から直列に
	// 呼ばれるため、sync.WaitGroup の Add/Wait のような再利用時の競合は
	// 生じない。
	runMu     sync.Mutex
	runDoneCh chan struct{}

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

	sentMu      sync.Mutex
	sentBuf     map[uint32]DataPointGroups // seqNum → 送信済みDataPointGroups
	keepPayload bool                       // true: Reliable (payload保存), false: Unreliable/Partial (payload除去)
	logger      log.Logger

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

	// Resumeトークン
	resumeToken string
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
	if beforeStatus != streamStatusResuming {
		if err := u.waitToSendAllDataPointsAndReceiveAllAck(ctx); err != nil {
			u.logger.Warnf(ctx, "Failed to waitSentAllDataPointsAndReceivedAllAck: %+v", err)
		}
	}
	return u.closeWithError(ctx, nil, opts...)
}

func (u *Upstream) closeWithError(ctx context.Context, causeError error, opts ...UpstreamCloseOption) error {
	if u.isClosed() {
		return nil
	}
	// 多重呼び出しガード。本関数には Close / resume 失敗 / flush の
	// validateState 失敗の 3 経路が到達し、flush が u.mu を離してから呼ぶ
	// ため並行に重なりうる。勝者だけが CloseRequest の送信と Closed
	// イベントの発火を行い、敗者は即座に nil を返す（従来の isClosed
	// 早期 return と同じ扱い）。u.cancel() を勝者の defer に限定している
	// のは、敗者が先に cancel すると勝者が送信中の RPC（内部経路は u.ctx
	// から派生した ctx を使う）を打ち切ってしまうため。
	if !u.closeRequested.CompareAndSwap(false, true) {
		return nil
	}
	defer u.cancel()

	// u.cancel() を呼べるのはこの勝者の defer だけ（u.ctx は親を持たず、
	// cancel の呼び出し元はここ 1 箇所）。したがって勝者自身の待ちを
	// u.ctx で守ると「自分が return しないと解除されない待ち」（自己参照）
	// になる。内部のエラー経路は closeWithErrorBounded を経由して
	// closeTimeout で上限を付けた ctx を渡すこと。

	opt := defaultUpstreamCloseOption
	for _, v := range opts {
		v(&opt)
	}

	// state の読み取りと wireConn の取得は u.mu の下で行う。dataIDAliases /
	// sendBuffer は u.mu の下で書き換わる map なので、ロック無しの走査は
	// fatal error: concurrent map iteration and map write で即死しうる
	// （Close / resume 失敗の経路はロック無しでここへ来ていた）。wireConn
	// も resume が u.mu の下で差し替えるためここで取得する
	// （sendChunkAndWaitAck と同型）。送信をロックの外に出しているのは、
	// u.mu を保持したままネットワーク往復すると受信経路
	// （processDataIDAliases / processResult 等）が芋づるで停止するため。
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

// closeWithErrorBounded は、内部のエラー経路（resume 失敗・flush の
// validateState 失敗）から closeWithError を呼ぶためのラッパーです。u.ctx を
// そのまま渡すと、u.cancel() を呼べるのは勝者の defer だけであるため、
// 送信がブロックしたとき自分の待ちを解除できる者がいなくなります
// （closeWithError のガードのコメント参照）。closeTimeout で上限を付けて
// この自己参照を切ります。利用者が Close(ctx) から入る経路は呼び出し元の
// ctx をそのまま使います。
func (u *Upstream) closeWithErrorBounded(causeError error) error {
	ctx, cancel := context.WithTimeout(u.ctx, u.closeTimeout)
	defer cancel()
	return u.closeWithError(ctx, causeError)
}

func (u *Upstream) waitToSendAllDataPointsAndReceiveAllAck(ctx context.Context) error {
	deadline := time.Now().Add(u.closeTimeout)
	parentCtx, cancel := context.WithCancel(u.ctx)
	defer cancel()
	parentCtx, cancel = context.WithDeadline(parentCtx, deadline)
	defer cancel()

	// Flush にも closeTimeout を効かせる。現時点では、flush 内の
	// validateState 失敗経路で flushLoop が長時間止まっても
	// closeWithErrorBounded が同じ closeTimeout で必ず解放するため実害は
	// ない。だが、flushLoop が closeWithErrorBounded を経由しない別要因で
	// 長時間ブロックするようになった場合に備え、Flush 自身にも呼び出し元
	// ctx を親にした期限を渡しておく。parentCtx は親が u.ctx なのでそのまま
	// 渡すと呼び出し元 ctx のキャンセルが効かなくなるため、呼び出し元 ctx を
	// 親にした期限付き ctx を別に作る。
	flushCtx, flushCancel := context.WithDeadline(ctx, deadline)
	defer flushCancel()
	if err := u.Flush(flushCtx); err != nil {
		return errors.Errorf("failed to flush chunk: %w", err)
	}

	alreadyReceivedLastSentAck := atomic.LoadUint32(&u.maxSequenceNumberInReceivedUpstreamChunkResults) == u.sequence.CurrentValue()
	if alreadyReceivedLastSentAck {
		return nil
	}

	// receivedAck.Wait() は ctx を見ないため、Ack が来なくなると parentCtx /
	// ctx の期限が来ても待ち続ける（Broadcast の唯一の発生源が processResult
	// なので、届かない Ack を待つ間は誰も起こさない）。期限で必ず起こす。
	watchdogDone := make(chan struct{})
	defer close(watchdogDone)
	go func() {
		select {
		case <-parentCtx.Done():
		case <-ctx.Done():
		case <-watchdogDone:
			return
		}
		// Broadcast は receivedAck.L を取ってから呼ぶ（processResult 側の
		// deferred Broadcast と同じ理由: 待ち手が条件観測後 Wait() に入る
		// までの窓での取りこぼしを防ぐ）。
		u.receivedAck.L.Lock()
		defer u.receivedAck.L.Unlock()
		u.receivedAck.Broadcast()
	}()

	u.receivedAck.L.Lock()
	defer u.receivedAck.L.Unlock()
	for {
		select {
		case <-parentCtx.Done():
			return errors.New("cannot receive final ack because already closed conn")
		case <-ctx.Done():
			return errors.New("receiving ack timed out")
		default:
		}
		remaining := u.listSent()

		u.mu.Lock()
		lengthSendBuffer := len(u.sendBuffer)
		u.mu.Unlock()
		if lengthSendBuffer == 0 && len(remaining) == 0 {
			return nil
		}
		u.receivedAck.Wait()
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

func (u *Upstream) run(isResume bool) error {
	done := make(chan struct{})
	u.runMu.Lock()
	u.runDoneCh = done
	u.runMu.Unlock()
	defer close(done)
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
	// readResultLoop / readAliasLoop も errgroup のメンバにして、run() が
	// 終了（それぞれの defer の完了）まで待つようにする。go で起動して
	// run() が終了を待たない形にすると、前世代のこれらが生き残ったまま
	// resume() が呼ばれ、遅れて走った readResultLoop の defer
	// （upstreamChunkResultChs の全 close + map 再作成）が次世代の live な
	// エントリを close してしまう（以降の Ack が !ok で無視され続け、
	// sentBuf が減らなくなるデータ欠損）。終了の連鎖は ctx cancel →
	// readAckLoop が return（defer で aliasCh / resCh を close）→
	// readResultLoop / readAliasLoop が range / 受信を抜けて defer を実行、
	// の順で成立し、デッドロックは生じない。
	eg.Go(func() error {
		u.readResultLoop(ctx)
		return nil
	})
	eg.Go(func() error {
		u.readAliasLoop(ctx)
		return nil
	})
	if isResume && u.Config.QoS == message.QoSReliable {
		eg.Go(func() error {
			m := u.listSent()
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
				// cap 1 は必須（バッファなしに戻さないこと）。根拠は processResult のコメント参照。
				resultCh := make(chan *message.UpstreamChunkResult, 1)
				u.mu.Lock()
				u.upstreamChunkResultChs[chunk.StreamChunk.SequenceNumber] = resultCh
				u.mu.Unlock()
				if err := u.sendChunkAndWaitAck(ctx, chunk, resultCh); err != nil {
					u.logger.Warnf(u.ctx, "%+v", err)
					u.mu.Lock()
					delete(u.upstreamChunkResultChs, chunk.StreamChunk.SequenceNumber)
					u.mu.Unlock()
				}
				u.logger.Debugf(u.ctx, "Resent data point groups[seqNum=%v, count=%v].", seqNum, len(dpg))
			}
			return nil
		})
	} else if isResume {
		u.clearSent()
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

	if len(u.sendBuffer) == 0 {
		u.mu.Unlock()
		return nil
	}

	if err := u.validateState(); err != nil {
		// closeWithError は内部で u.mu.RLock を取る。非再入の u.mu を
		// 保持したまま呼ぶと自己デッドロックする上、ロック保持のまま
		// CloseRequest のネットワーク往復に入ってしまうため、先に離す。
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

	// cap 1 は必須（バッファなしに戻さないこと）。根拠は processResult のコメント参照。
	resultCh := make(chan *message.UpstreamChunkResult, 1)
	u.upstreamChunkResultChs[msgChunk.StreamChunk.SequenceNumber] = resultCh
	u.mu.Unlock()

	go u.sendChunkAndWaitAck(ctx, msgChunk, resultCh)
	return nil
}

func (u *Upstream) sendChunkAndWaitAck(ctx context.Context, msgChunk *message.UpstreamChunk, resultCh chan *message.UpstreamChunkResult) error {
	u.mu.RLock()
	wireConn := u.wireConn
	u.mu.RUnlock()
	err := wireConn.SendUpstreamChunk(u.ctx, msgChunk)
	if err != nil {
		return fmt.Errorf("failed to send upstream chunk[seq:%v]: %w", msgChunk.StreamChunk.SequenceNumber, err)
	}

	timeoutCh := u.withAckTimeoutCh(ctx, resultCh)

	result, ok := <-timeoutCh
	if !ok {
		return fmt.Errorf("upstream chunk result channel closed unexpectedly")
	}

	// この L は maxSequenceNumberInReceivedUpstreamChunkResults の
	// load-compare-store を守るためだけに残す。sentBuf からの削除と
	// drain への Broadcast は Ack 到着（processResult）側に一本化した
	// ため、ここでは行わない。
	u.receivedAck.L.Lock()
	defer u.receivedAck.L.Unlock()

	if result != nil && atomic.LoadUint32(&u.maxSequenceNumberInReceivedUpstreamChunkResults) < msgChunk.StreamChunk.SequenceNumber {
		atomic.StoreUint32(&u.maxSequenceNumberInReceivedUpstreamChunkResults, msgChunk.StreamChunk.SequenceNumber)
	}

	return nil
}

func (u *Upstream) withAckTimeoutCh(ctx context.Context, inCh <-chan *message.UpstreamChunkResult) <-chan *message.UpstreamChunkResult {
	resCh := make(chan *message.UpstreamChunkResult)
	go func() {
		defer close(resCh)
		timeoutCtx, cancel := ctx, context.CancelFunc(func() {})
		if u.Config.AckTimeout != 0 {
			timeoutCtx, cancel = context.WithTimeout(ctx, u.Config.AckTimeout)
		}
		defer cancel()
		select {
		case <-timeoutCtx.Done():
			select {
			case <-ctx.Done():
			case <-u.ctx.Done():
			case resCh <- nil:
			}
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

// readAckLoop は ack を aliasCh / resCh へ中継する。readResultLoop /
// readAliasLoop はここではなく run() の errgroup で起動する。go で起動して
// run() が終了を待たない形にすると、前セッション世代のこれらが生き残った
// まま resume() が呼ばれ、遅れて走った readResultLoop の defer
// （upstreamChunkResultChs の全 close + map 再作成）が次世代の live な
// エントリを close してしまう（Ack 無視によるデータ欠損）。
func (u *Upstream) readAckLoop(ctx context.Context) {
	defer func() {
		u.mu.Lock()
		close(u.aliasCh)
		close(u.resCh)
		u.mu.Unlock()
	}()

	for ack := range u.ackOrDone(ctx) {
		// 裸送信にしないこと。読み手（readResultLoop / readAliasLoop）が
		// u.mu の競合などで停止していると aliasCh / resCh（いずれも cap 8）
		// が満杯のまま解けないことがあり、ctx を見ない送信はそこで永久に
		// ブロックする。readAckLoop は run() の errgroup メンバなので、
		// ここが返らないと eg.Wait() が完了せず、Conn の再接続全体が
		// 止まる。
		select {
		case u.aliasCh <- ack.DataIDAliases:
		case <-ctx.Done():
			return
		}
		select {
		case u.resCh <- ack.Results:
		case <-ctx.Done():
			return
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

func (u *Upstream) readAliasLoop(ctx context.Context) {
	for {
		u.mu.RLock()
		aliasCh := u.aliasCh
		u.mu.RUnlock()
		if aliasCh == nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case v, ok := <-aliasCh:
			if !ok {
				return
			}
			u.processDataIDAliases(v)
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
	// Ack が届いた時点で未 ack 集合から外す。待ち手（sendChunkAndWaitAck）が
	// ack タイムアウトや送信エラーで既に離脱していても、Ack が届いた以上
	// sentBuf からは消えなければならない。
	// 下の !ok 早期 return より前に置くこと。配送済み seq の 2 通目の Ack と、
	// run() の再送ループが送信失敗でエントリを消した chunk の Ack は !ok に
	// 落ちるが、いずれも sentBuf からは外す必要がある。
	u.removeSent(result.SequenceNumber)

	// drain の wakeup。u.mu.Lock() より前に defer 登録することで、defer の
	// LIFO により u.mu.Unlock() の後に走る。u.mu 保持中に receivedAck.L を
	// 取ると drain 側（receivedAck.L -> u.mu）と ABBA になるため必須。
	defer func() {
		u.receivedAck.L.Lock()
		defer u.receivedAck.L.Unlock()
		u.receivedAck.Broadcast()
	}()

	u.mu.Lock()
	defer u.mu.Unlock()
	ch, ok := u.upstreamChunkResultChs[result.SequenceNumber]
	if !ok {
		return nil
	}
	// ch は cap 1 なので、この送信は必ず即完了する。各チャネルへの送信は
	// 最大 1 回（送信後に delete し、同一 seq の 2 回目以降の Ack は上の !ok で
	// 無視される）だからである。
	//
	// バッファなしに戻してはいけない: sendChunkAndWaitAck は送信エラー /
	// ack タイムアウト / u.ctx キャンセルの各経路で、
	// upstreamChunkResultChs のエントリを残したまま Ack を待たずに return する。
	// その後に当該 seq の Ack が届くと、読み手のいないチャネルへの送信が
	// u.mu（writer）を保持したままブロックし、flush / WriteDataPoints / Close /
	// readResultLoop（以降の Ack 処理すべて）が連鎖して止まる。「送信は失敗
	// したがサーバーは Ack を返す」は、multi transport の部分送信済みエラーや
	// write timeout（送信バッファ投入後のエラー）で実在する。
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
	u.mu.Lock()
	u.wireConn = newConn
	u.mu.Unlock()

	// ResumeTokenサポート判定
	// v3.0.0以降: 保存されたトークンを使用
	// v2.x.x: 空文字列を送信
	supportsResumeToken := newConn.SupportsResumeToken()

	var resumeToken string
	if supportsResumeToken {
		resumeToken = u.resumeToken
	}

	var resp *message.UpstreamResumeResponse
	var resErr error

	retry.DoWithContext(u.ctx, func() (end bool) {
		resp, resErr = newConn.SendUpstreamResumeRequest(u.ctx, &message.UpstreamResumeRequest{
			StreamID:    u.ID,
			ResumeToken: resumeToken,
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
	if resp == nil && resErr == nil {
		// u.ctx のキャンセルにより、応答を一度も得ないまま打ち切られた。
		// ここで弾かないと後続の resp.AssignedStreamIDAlias で nil 参照になる。
		resErr = errors.ErrConnectionClosed
	}
	if resErr != nil {
		u.closeWithErrorBounded(resErr)
		return errors.Errorf("failed send upstream resume request: %w", resErr)
	}

	ch, err := newConn.SubscribeUpstreamChunkAck(u.ctx, resp.AssignedStreamIDAlias)
	if err != nil {
		return errors.Errorf("failed to SubscribeUpstreamChunkAck: %w", err)
	}

	u.mu.Lock()
	u.ackCh = ch
	u.aliasCh = make(chan map[uint32]*message.DataID, 8)
	u.resCh = make(chan []*message.UpstreamChunkResult, 8)
	u.idAlias = resp.AssignedStreamIDAlias
	// v3.0.0以降: 新しいトークンを保存
	// v2.x.x: resumeTokenは更新しない
	if supportsResumeToken {
		u.resumeToken = resp.ResumeToken
	}
	u.mu.Unlock()

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
