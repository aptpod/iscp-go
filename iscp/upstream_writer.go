package iscp

import (
	"context"
	"sync/atomic"

	"github.com/aptpod/iscp-go/errors"
	"github.com/aptpod/iscp-go/message"
)

// UpstreamWriter は、特定のDataIDに対するデータポイント書き込みを行うWriterです。
type UpstreamWriter struct {
	dataID   *message.DataID
	upstream *Upstream
	closed   atomic.Bool
}

// Write は、データポイントを内部バッファに書き込みます。
func (w *UpstreamWriter) Write(ctx context.Context, dps ...*message.DataPoint) error {
	if w.closed.Load() {
		return errors.New("writer is closed")
	}
	return w.upstream.WriteDataPoints(ctx, w.dataID, dps...)
}

// Close は、Writerを閉じます。
// Close はブロックしない。バッファ内のデータは次の Flush で送信される。
func (w *UpstreamWriter) Close() error {
	if w.closed.Swap(true) {
		return errors.New("writer already closed")
	}
	return nil
}
