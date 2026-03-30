package iscp

import (
	"context"
	"sync/atomic"

	"github.com/aptpod/iscp-go/v2/errors"
)

const defaultReaderChBufferSize = 256

// DownstreamReader は、フィルタ条件に合致するDataPointを1件ずつ読み取るReaderです。
type DownstreamReader struct {
	ctx        context.Context
	cancel     context.CancelFunc
	ch         chan *DownstreamDataPoint
	filterIdx  uint32
	downstream *Downstream
	closed     atomic.Bool
}

// Read は、次のDataPointを1件読み取ります。データがない場合はブロックします。
func (r *DownstreamReader) Read(ctx context.Context) (*DownstreamDataPoint, error) {
	if r.closed.Load() {
		return nil, errors.New("reader is closed")
	}
	select {
	case <-r.downstream.ctx.Done():
		return nil, errors.ErrStreamClosed
	case <-r.ctx.Done():
		return nil, r.ctx.Err()
	case <-ctx.Done():
		return nil, ctx.Err()
	case dp, ok := <-r.ch:
		if !ok {
			return nil, errors.ErrStreamClosed
		}
		return dp, nil
	}
}

// Close は、Readerを閉じてdemuxerへの登録を解除します。
func (r *DownstreamReader) Close() error {
	if r.closed.Swap(true) {
		return errors.New("reader already closed")
	}
	r.cancel()
	r.downstream.unregisterReader(r)
	return nil
}
