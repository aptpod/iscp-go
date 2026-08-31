package iscp

import (
	"context"
	"testing"
	"time"

	uuid "github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/aptpod/iscp-go/log"
	"github.com/aptpod/iscp-go/message"
)

// TestDownstreamCloseUsesTimeoutForFinalAckFlushは、Downstream.Close(ctx)が、
// 最終Ackのフラッシュが完了しない状況でも、closeTimeout（既定はUpstreamと共用の
// defaultCloseTimeout）で打ち切られて返ることを検証します。
//
// RED理由: 修正前はcloseWithError内のselectがctx.Done()（呼び出し元がキャンセル
// しない限り発火しない）を待つだけで、closeTimeout相当の上限がありません。
// finalAckFlushedが一切closeされない状況（本テストではrun()を起動せず、
// flushAckLoopが動いていないため意図的にcloseされない）では、Closeは
// テストのタイムアウトまで戻ってきません。
func TestDownstreamCloseUsesTimeoutForFinalAckFlush(t *testing.T) {
	wireConn, srv := newTestClientConnPair(t)

	srvDone := make(chan struct{})
	go func() {
		defer close(srvDone)
		// Ping/Pongは無視しつつ、DownstreamCloseRequestにだけ応答する。
		for {
			msg := mustReadIgnoringPingPong(t, srv)
			req, ok := msg.(*message.DownstreamCloseRequest)
			if !ok {
				continue
			}
			_ = srv.Write(&message.DownstreamCloseResponse{
				RequestID:    req.RequestID,
				ResultCode:   message.ResultCodeSucceeded,
				ResultString: "OK",
			})
			return
		}
	}()

	const closeTimeout = 100 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := &Downstream{
		ctx:             ctx,
		cancel:          cancel,
		ID:              uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		wireConn:        wireConn,
		closeTimeout:    closeTimeout,
		finalAckFlushed: make(chan struct{}), // run()を起動しないため意図的にcloseしないままにする
		state:           newStreamState(),
		eventDispatcher: newEventDispatcher(),
		logger:          log.NewNop(),
		Config:          defaultDownstreamConfig,
	}

	started := time.Now()
	done := make(chan error, 1)
	go func() {
		done <- d.Close(context.Background())
	}()

	select {
	case err := <-done:
		require.NoError(t, err)
		elapsed := time.Since(started)
		require.GreaterOrEqual(t, elapsed, closeTimeout, "Close should wait at least closeTimeout for the final ack flush")
		require.Less(t, elapsed, 2*time.Second, "Close should return promptly once the close timeout elapses")
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return within the expected close timeout")
	}

	<-srvDone
}
