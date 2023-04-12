package iscp

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/aptpod/iscp-go/errors"
	"github.com/aptpod/iscp-go/internal/retry"

	"github.com/aptpod/iscp-go/log"
	"github.com/aptpod/iscp-go/message"
	"github.com/aptpod/iscp-go/wire"
	uuid "github.com/google/uuid"
	"golang.org/x/sync/errgroup"
)

var defaultAckFlushInterval = time.Millisecond * 100

// DownstreamStateは、ダウンストリームの状態です。
type DownstreamState struct {
	// データIDエイリアスとデータIDのマップ
	DataIDAliases map[uint32]*message.DataID

	// 最後に払い出されたデータIDエイリアス
	LastIssuedDataIDAlias uint32

	// アップストリームエイリアスとアップストリーム情報のマップ
	UpstreamInfos map[uint32]*message.UpstreamInfo

	// 最後に払い出されたアップストリーム情報のエイリアス
	LastIssuedUpstreamInfoAlias uint32

	// 最後に払い出されたAckのID
	LastIssuedChunkAckID uint32
}

// Downstreamは、ダウンストリームです。
type Downstream struct {
	ID         uuid.UUID        // ID
	ServerTime time.Time        // DownstreamOpenResponseで返却されたサーバー時刻
	Config     DownstreamConfig // Downstreamの設定

	mu     sync.RWMutex
	ctx    context.Context
	cancel context.CancelFunc

	dataIDAliases               map[uint32]*message.DataID       // データIDエイリアスとデータIDのマップ
	revDataIDAliases            map[message.DataID]uint32        // データIDとデータIDエイリアスのマップ
	lastIssuedDataIDAlias       uint32                           // 最後に払い出されたデータIDエイリアス
	upstreamInfos               map[uint32]*message.UpstreamInfo // アップストリームエイリアスとアップストリーム情報のマップ
	lastIssuedUpstreamInfoAlias uint32                           // 最後に払い出されたアップストリーム情報のエイリアス
	lastIssuedAckSequenceNumber uint32                           // 最後に払い出されたAckのシーケンス番号

	wireConn     *wire.ClientConn
	idAlias      uint32
	dpsCh        <-chan *message.DownstreamChunk
	metaCh       <-chan *message.DownstreamMetadata
	ackCompCh    <-chan *message.DownstreamChunkAckComplete
	dataPointsCh chan *DownstreamChunk
	metadataCh   chan *DownstreamMetadata
	logger       log.Logger

	dataIDAliasGenerator *wire.AliasGenerator

	upstreamInfoAliasGenerator *wire.AliasGenerator

	ackFlushInterval      time.Duration
	upstreamInfoAckBuffer map[uint32]*message.UpstreamInfo
	dataIDAckBuffer       map[uint32]*message.DataID
	resultAckBuffer       []*message.DownstreamChunkResult
	chunkAckIDSequence    *sequenceNumberGenerator
	finalAckFlushed       chan struct{}

	state           *streamState
	connStatus      *connStatus
	eventDispatcher *eventDispatcher
}

// Stateは、Downstreamが保持している内部の状態を返却します。
func (d *Downstream) State() *DownstreamState {
	d.mu.Lock()
	defer d.mu.Unlock()

	var res DownstreamState
	// copy DataIDAlias
	res.DataIDAliases = make(map[uint32]*message.DataID, len(d.dataIDAliases))
	for k, v := range d.dataIDAliases {
		vv := *v
		res.DataIDAliases[k] = &vv
	}
	// copy UpstreamInfos
	res.UpstreamInfos = make(map[uint32]*message.UpstreamInfo, len(d.upstreamInfos))
	for k, v := range d.upstreamInfos {
		vv := *v
		res.UpstreamInfos[k] = &vv
	}
	res.LastIssuedChunkAckID = d.chunkAckIDSequence.CurrentValue()
	res.LastIssuedDataIDAlias = d.dataIDAliasGenerator.CurrentValue()
	res.LastIssuedUpstreamInfoAlias = d.upstreamInfoAliasGenerator.CurrentValue()
	return &res
}

// Closeは、ダウンストリームを閉じます。
func (d *Downstream) Close(ctx context.Context) (err error) {
	return d.closeWithError(ctx, nil)
}

func (d *Downstream) closeWithError(ctx context.Context, cause error) (err error) {
	defer d.cancel()
	if d.isClosed() {
		return nil
	}
	beforeStatus := d.state.Swap(streamStatusDraining)
	if beforeStatus == streamStatusDraining {
		return errors.New("already draining")
	}

	if beforeStatus != streamStatusResuming {
		select {
		case <-d.ctx.Done():
			d.logger.Warnf(ctx, "close parent conn")
		case <-ctx.Done():
			d.logger.Warnf(ctx, "final ack flush dead line elapsed")
		case <-d.finalAckFlushed:
		}
	}

	resp, err := d.wireConn.SendDownstreamCloseRequest(ctx, &message.DownstreamCloseRequest{
		StreamID: d.ID,
	})
	if err != nil {
		return errors.Errorf("failed to SendDownstreamCloseRequest: %w", err)
	}

	if resp.ResultCode != message.ResultCodeSucceeded {
		return errors.FailedMessageError{
			ResultCode:      resp.ResultCode,
			ResultString:    resp.ResultString,
			ReceivedMessage: resp,
		}
	}

	defer d.eventDispatcher.addHandler(func() {
		d.Config.ClosedEventHandler.OnDownstreamClosed(&DownstreamClosedEvent{
			Config: d.Config,
			State:  *d.State(),
			Err:    cause,
		})
	})

	return nil
}

// ReadDataPointsは、ダウンストリームデータポイントを受信します。
func (d *Downstream) ReadDataPoints(ctx context.Context) (*DownstreamChunk, error) {
	select {
	case <-d.ctx.Done():
		return nil, errors.ErrStreamClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-d.dataPointsCh:
		return res, nil
	}
}

// ReadMetadataは、ダウンストリームメタデータを受信します。
func (d *Downstream) ReadMetadata(ctx context.Context) (*DownstreamMetadata, error) {
	select {
	case <-d.ctx.Done():
		return nil, errors.ErrStreamClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-d.metadataCh:
		return res, nil
	}
}

func (d *Downstream) run() error {
	ctx, cancel := context.WithCancel(d.ctx)
	defer cancel()
	eg, ctx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		defer d.eventDispatcher.cond.Broadcast()
		defer d.state.cond.Broadcast()
		<-ctx.Done()
		return nil
	})

	eg.Go(func() error {
		d.readDataPointsLoop(ctx)
		return nil
	})

	eg.Go(func() error {
		d.readMetadataLoop(ctx)
		return nil
	})

	eg.Go(func() error {
		d.flushAckLoop(ctx)
		return nil
	})

	eg.Go(func() error {
		d.readAckCompleteLoop(ctx)
		return nil
	})

	eg.Go(func() error {
		d.connStatus.cond.L.Lock()
		for !d.connStatus.IsWithoutLock(connStatusReconnecting) {
			select {
			case <-ctx.Done():
				d.connStatus.cond.L.Unlock()
				return nil
			default:
			}
			d.connStatus.cond.Wait()
		}
		d.connStatus.cond.L.Unlock()
		d.state.Swap(streamStatusResuming)
		return errors.New("unexpected disconnected")
	})
	return eg.Wait()
}

func (d *Downstream) flushAckLoop(ctx context.Context) {
	ticker := time.NewTicker(d.ackFlushInterval)
	defer ticker.Stop()
	defer close(d.finalAckFlushed)
	defer d.flushAck()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		defer cancel()
		d.state.cond.L.Lock()
		for d.state.CurrentWithoutLock() != streamStatusDraining {
			select {
			case <-ctx.Done():
				d.state.cond.L.Unlock()
				return
			default:
			}
			d.state.cond.Wait()
		}
		d.state.cond.L.Unlock()
	}()

	for {
		select {
		case <-ticker.C:
			d.flushAck()
		case <-ctx.Done():
			d.flushAck()
			return
		}
	}
}

func (d *Downstream) pushUpstreamInfoAckBuffer(m map[uint32]*message.UpstreamInfo) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for k, v := range m {
		d.upstreamInfoAckBuffer[k] = v
	}
}

func (d *Downstream) pushDataIDAckBuffer(m map[uint32]*message.DataID) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for k, v := range m {
		d.dataIDAckBuffer[k] = v
	}
}

func (d *Downstream) pushResultAckBuffer(res *message.DownstreamChunkResult) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.resultAckBuffer = append(d.resultAckBuffer, res)
}

func (d *Downstream) flushAck() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if len(d.dataIDAckBuffer) == 0 && len(d.resultAckBuffer) == 0 && len(d.upstreamInfoAckBuffer) == 0 {
		return nil
	}

	ack := &message.DownstreamChunkAck{
		StreamIDAlias:   d.idAlias,
		AckID:           d.chunkAckIDSequence.Next(),
		UpstreamAliases: d.upstreamInfoAckBuffer,
		DataIDAliases:   d.dataIDAckBuffer,
		Results:         d.resultAckBuffer,
	}

	d.upstreamInfoAckBuffer = make(map[uint32]*message.UpstreamInfo)
	d.dataIDAckBuffer = make(map[uint32]*message.DataID)
	d.resultAckBuffer = make([]*message.DownstreamChunkResult, 0)

	return d.wireConn.SendDownstreamDatapointsAck(d.ctx, ack)
}

func (d *Downstream) ackCompleteOrDone(ctx context.Context) <-chan *message.DownstreamChunkAckComplete {
	res := make(chan *message.DownstreamChunkAckComplete)
	go func() {
		defer close(res)
		for {
			select {
			case c := <-d.ackCompCh:
				res <- c
			case <-ctx.Done():
				return
			}
		}
	}()
	return res
}

func (d *Downstream) readAckCompleteLoop(ctx context.Context) {
	for ack := range d.ackCompleteOrDone(ctx) {
		// todo
		if ack.ResultCode != message.ResultCodeSucceeded {
			d.logger.Warnf(d.ctx, "ack error: %v", ack.ResultString)
		}
	}
}

func (d *Downstream) dataPointOrDone(ctx context.Context) <-chan *message.DownstreamChunk {
	res := make(chan *message.DownstreamChunk)
	go func() {
		defer close(res)
		for {
			select {
			case c := <-d.dpsCh:
				res <- c
			case <-ctx.Done():
				return
			}
		}
	}()
	return res
}

func (d *Downstream) readMetadataLoop(ctx context.Context) {
	for meta := range d.metadataOrDone(ctx) {
		select {
		case d.metadataCh <- &DownstreamMetadata{
			SourceNodeID: meta.SourceNodeID,
			Metadata:     meta.Metadata,
		}:
		default:
		}
	}
}

func (d *Downstream) metadataOrDone(ctx context.Context) <-chan *message.DownstreamMetadata {
	res := make(chan *message.DownstreamMetadata)
	go func() {
		defer close(res)
		for {
			select {
			case c, ok := <-d.metaCh:
				if !ok {
					return
				}
				res <- c
			case <-ctx.Done():
				return
			}
		}
	}()
	return res
}

func (d *Downstream) readDataPointsLoop(ctx context.Context) {
	for dps := range d.dataPointOrDone(ctx) {

		d.processUpstreamAlias(dps.UpstreamOrAlias)
		d.processDataPoints(dps.StreamChunk.DataPointGroups)

		ps, err := d.wireToDownstreamChunk(dps)
		if err != nil {
			d.logger.Errorf(d.ctx, "protocol error: %+v", err)
			continue
		}

		d.pushResultAckBuffer(&message.DownstreamChunkResult{
			ResultCode:               message.ResultCodeSucceeded,
			ResultString:             "OK",
			SequenceNumberInUpstream: dps.StreamChunk.SequenceNumber,
			StreamIDOfUpstream:       ps.UpstreamInfo.StreamID,
		})

		select {
		case d.dataPointsCh <- ps:
		default:
		}
	}
}

func filterDataID(gs []*message.DownstreamDataPointGroup) []*message.DataID {
	res := make([]*message.DataID, 0)
	for _, v := range gs {
		switch t := v.DataIDOrAlias.(type) {
		case *message.DataID:
			res = append(res, t)
		default:
			continue
		}
	}
	return res
}

func (d *Downstream) wireToDownstreamChunk(dps *message.DownstreamChunk) (*DownstreamChunk, error) {
	var info message.UpstreamInfo
	switch t := dps.UpstreamOrAlias.(type) {
	case message.UpstreamAlias:
		d.mu.RLock()
		i, ok := d.upstreamInfos[uint32(t)]
		d.mu.RUnlock()
		if !ok {
			return nil, errors.New("invalid upstream info alias")
		}
		info = *i
	case *message.UpstreamInfo:
		info = *t
	default:
		panic("unreachable")
	}

	dpgs := make(DataPointGroups, 0)
	for _, v := range dps.StreamChunk.DataPointGroups {
		var id message.DataID
		switch t := v.DataIDOrAlias.(type) {
		case *message.DataID:
			id = *t
		case message.DataIDAlias:
			d.mu.RLock()
			i, ok := d.dataIDAliases[uint32(t)]
			d.mu.RUnlock()

			if !ok {
				return nil, errors.New("invalid data id alias")
			}
			id = *i
		default:
			panic("unreachable")
		}
		dpgs = append(dpgs, &DataPointGroup{
			DataID:     &id,
			DataPoints: v.DataPoints,
		})
	}

	return &DownstreamChunk{
		SequenceNumber:  dps.StreamChunk.SequenceNumber,
		UpstreamInfo:    &info,
		DataPointGroups: dpgs,
	}, nil
}

func (d *Downstream) processDataPoints(gs []*message.DownstreamDataPointGroup) {
	d.pushDataIDAckBuffer(d.assignDataIDAlias(filterDataID(gs)))
}

func (d *Downstream) assignDataIDAlias(ids []*message.DataID) map[uint32]*message.DataID {
	d.mu.Lock()
	defer d.mu.Unlock()
	res := make(map[uint32]*message.DataID)

	for _, id := range ids {
		if _, ok := d.revDataIDAliases[*id]; !ok {
			a := d.dataIDAliasGenerator.Next()
			d.dataIDAliases[a] = id
			d.revDataIDAliases[*id] = a
			res[a] = id
		}
	}
	return res
}

func (d *Downstream) processUpstreamAlias(a message.UpstreamOrAlias) {
	switch t := a.(type) {
	case *message.UpstreamInfo:
		m := d.assignUpstreamInfoAlias(t)
		d.pushUpstreamInfoAckBuffer(m)
		return
	default:
		return
	}
}

func (d *Downstream) assignUpstreamInfoAlias(info *message.UpstreamInfo) map[uint32]*message.UpstreamInfo {
	d.mu.Lock()
	defer d.mu.Unlock()

	for _, v := range d.upstreamInfos {
		if v == info {
			// already assigned
			return nil
		}
	}
	a := d.upstreamInfoAliasGenerator.Next()
	d.upstreamInfos[a] = info

	return map[uint32]*message.UpstreamInfo{
		a: info,
	}
}

func (d *Downstream) isClosed() bool {
	select {
	case <-d.ctx.Done():
		return true
	default:
		return false
	}
}

func (d *Downstream) resume(parentConn *Conn) error {
	d.logger.Infof(d.ctx, "Downstream start resuming [%s]", d.ID)
	if d.isClosed() {
		return fmt.Errorf("already closed downstream")
	}
	if !d.state.Is(streamStatusResuming) {
		return fmt.Errorf("invalid state want[%v] but[%v]", streamStatusResuming, d.state)
	}
	d.wireConn = parentConn.wireConn

	var resErr error
	retry.Do(func() (end bool) {
		dpsCh, err := d.wireConn.SubscribeDownstreamChunk(d.ctx, d.idAlias, d.Config.QoS)
		if err != nil {
			resErr = fmt.Errorf("failed to SubscribeDownstreamChunk: %w", err)
			return true
		}
		ackCompCh, err := d.wireConn.SubscribeDownstreamChunkAckComplete(d.ctx, d.idAlias)
		if err != nil {
			resErr = fmt.Errorf("failed to SubscribeDownstreamChunkAckComplete: %w", err)
			return true
		}

		metaCh, err := parentConn.subscribeDownstreamMetadata(d.ctx, d.idAlias, d.Config.Filters)
		if err != nil {
			resErr = fmt.Errorf("failed to subscribeDownstreamMetadata: %w", err)
			return true
		}

		resp, err := d.wireConn.SendDownstreamResumeRequest(d.ctx, &message.DownstreamResumeRequest{
			StreamID:             d.ID,
			DesiredStreamIDAlias: d.idAlias,
		})
		if err != nil {
			resErr = fmt.Errorf("failed to SendDownstreamResumeRequest: %w", err)
			return true
		}

		if resp.ResultCode == message.ResultCodeResumeRequestConflict {
			return false
		}

		if resp.ResultCode != message.ResultCodeSucceeded {
			resErr = &errors.FailedMessageError{
				ResultCode:      resp.ResultCode,
				ResultString:    resp.ResultString,
				ReceivedMessage: resp,
			}
			return true
		}
		resErr = nil
		d.dpsCh = dpsCh
		d.ackCompCh = ackCompCh
		d.metaCh = metaCh
		d.finalAckFlushed = make(chan struct{})

		return true
	})
	if resErr != nil {
		d.closeWithError(d.ctx, resErr)
		return resErr
	}
	d.eventDispatcher.addHandler(func() {
		d.Config.ResumedEventHandler.OnDownstreamResumed(&DownstreamResumedEvent{
			ID:     d.ID,
			Config: d.Config,
			State:  *d.State(),
		})
	})
	d.state.Swap(streamStatusConnected)
	return nil
}
