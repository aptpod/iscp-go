package iscp_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	. "github.com/aptpod/iscp-go/iscp"
	"github.com/aptpod/iscp-go/message"
	"github.com/aptpod/iscp-go/transport"
)

// TestUpstream_AckTimeout_PendingChunkResentOnResume_LateAckedChunkNotDuplicated は、
// Ack タイムアウトで sendChunkAndWaitAck の待ち手が離脱した chunk が sentBuf から
// 消えず、resume 時に再送対象になることを検証する（データ欠損の回帰防止）。
// あわせて、待ち手離脱後に届いた遅延 Ack はきちんと反映され、その chunk が
// resume 時に重複再送されないことも検証する。
//
// 発火条件: sendChunkAndWaitAck が AckTimeout で timeoutCh から nil を受け取ると、
// 修正前は無条件で u.removeSent(...) を呼んでいた。Ack が実際には届いていない
// chunk（seq=1）まで sentBuf から消えるため、resume 時に再送されずデータが失われる。
//
// オラクル: 2 回目のトランスポート（ds[1]）が resume 後に受け取る UpstreamChunk が
// seq=1 のみであること（seq=2 は resume 前に実 Ack で removeSent 済みなので届かない）。
// 修正前は seq=1 が sentBuf から消えているため resume 後に一切再送されず、
// このサーバーゴルーチンは UpstreamChunk を待ち続けたまま UpstreamCloseRequest を
// 受け取れず FAIL（タイムアウト）する。
func TestUpstream_AckTimeout_PendingChunkResentOnResume_LateAckedChunkNotDuplicated(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	ctx := context.Background()

	ds := []*dialer{newDialer(transport.NegotiationParams{}), newDialer(transport.NegotiationParams{})}
	registerTestTransport(t, ds)

	ackTimeout := 50 * time.Millisecond
	lateAckReady := make(chan struct{})

	d0Done := make(chan struct{})
	go func() {
		defer close(d0Done)
		d := ds[0]
		mockConnectRequest(t, d.srv)
		openReq := mustReadIgnorePingPong(t, d.srv).(*message.UpstreamOpenRequest)
		mustWrite(t, d.srv, &message.UpstreamOpenResponse{
			RequestID:             openReq.RequestID,
			AssignedStreamID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			AssignedStreamIDAlias: 1,
			ResultCode:            message.ResultCodeSucceeded,
			ResultString:          "OK",
			DataIDAliases:         map[uint32]*message.DataID{},
		})

		// chunk(seq=1) は読むが Ack は一切送らない。AckTimeout 発火後も
		// sentBuf に残ったままであることを resume 時に確認する。
		//
		// この goroutine 内の検証には assert を使うこと: require は
		// FailNow (runtime.Goexit) を呼ぶが、テスト本体のゴルーチン以外から
		// 呼ぶと後続のチャネル待ちが解放されずテスト全体がハングする。
		first := mustReadIgnorePingPong(t, d.srv).(*message.UpstreamChunk)
		assert.EqualValues(t, 1, first.StreamChunk.SequenceNumber)

		// chunk(seq=2) は読むが、待ち手が AckTimeout で離脱した後まで Ack を
		// 遅らせる。遅延 Ack が届いても removeSent が効くことを確認する。
		second := mustReadIgnorePingPong(t, d.srv).(*message.UpstreamChunk)
		assert.EqualValues(t, 2, second.StreamChunk.SequenceNumber)

		<-lateAckReady
		mustWrite(t, d.srv, &message.UpstreamChunkAck{
			StreamIDAlias: 1,
			Results: []*message.UpstreamChunkResult{
				{SequenceNumber: 2, ResultCode: message.ResultCodeSucceeded, ResultString: "OK"},
			},
			DataIDAliases:   map[uint32]*message.DataID{},
			ExtensionFields: &message.UpstreamChunkAckExtensionFields{},
		})
	}()

	d1Done := make(chan struct{})
	go func() {
		defer close(d1Done)
		d := ds[1]
		mockConnectRequest(t, d.srv)
		msg := mustReadIgnorePingPong(t, d.srv)
		req, ok := msg.(*message.UpstreamResumeRequest)
		if !assert.True(t, ok, "%T", msg) {
			return
		}
		mustWrite(t, d.srv, &message.UpstreamResumeResponse{
			RequestID:             req.RequestID,
			AssignedStreamIDAlias: 1,
			ResultCode:            message.ResultCodeSucceeded,
			ResultString:          "OK",
			ExtensionFields:       &message.UpstreamResumeResponseExtensionFields{},
		})

		var resentSeq1 bool
		for {
			msg := mustReadIgnorePingPong(t, d.srv)
			switch m := msg.(type) {
			case *message.UpstreamChunk:
				// seq=2 はここに来てはいけない: resume 前に実 Ack で
				// removeSent 済みなので再送対象から外れているはず。
				assert.EqualValues(t, 1, m.StreamChunk.SequenceNumber,
					"acked chunk must not be resent duplicately")
				resentSeq1 = true
				mustWrite(t, d.srv, &message.UpstreamChunkAck{
					StreamIDAlias: 1,
					Results: []*message.UpstreamChunkResult{
						{SequenceNumber: m.StreamChunk.SequenceNumber, ResultCode: message.ResultCodeSucceeded, ResultString: "OK"},
					},
					DataIDAliases:   map[uint32]*message.DataID{},
					ExtensionFields: &message.UpstreamChunkAckExtensionFields{},
				})
				continue
			case *message.UpstreamCloseRequest:
				// seq=1 は Ack 未受信のまま resume を迎えたので、必ず再送
				// されていなければならない（データ欠損の回帰防止）。
				assert.True(t, resentSeq1, "unacked chunk (seq=1) must be resent on resume")
				mustWrite(t, d.srv, &message.UpstreamCloseResponse{
					RequestID:    m.RequestID,
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "OK",
				})
			}
			break
		}
	}()

	conn, err := Connect("dummy", TransportTest)
	require.NoError(t, err)
	defer conn.Close(ctx)

	hooker := NewCaptureHooker()
	var capture hookerAndEventHandler
	up, err := conn.OpenUpstream(ctx,
		"session_id",
		WithUpstreamQoS(message.QoSReliable),
		WithUpstreamFlushPolicyImmediately(),
		WithUpstreamAckTimeout(ackTimeout),
		WithUpstreamReceiveAckHooker(hooker),
		WithUpstreamResumedEventHandler(UpstreamResumedEventHandlerFunc(capture.UpstreamResumed)),
	)
	require.NoError(t, err)
	defer up.Close(ctx)

	dataID := &message.DataID{Name: "name", Type: "type"}
	dp := &message.DataPoint{ElapsedTime: time.Second, Payload: []byte{1, 2, 3, 4}}

	require.NoError(t, up.WriteDataPoints(ctx, dataID, dp)) // chunk seq=1（Ack は届かない）
	time.Sleep(10 * time.Millisecond)
	require.NoError(t, up.WriteDataPoints(ctx, dataID, dp)) // chunk seq=2（遅延 Ack）

	// 両チャンクの AckTimeout 発火を保証してから、seq=2 の遅延 Ack を送らせる。
	time.Sleep(ackTimeout + 250*time.Millisecond)
	close(lateAckReady)

	// seq=2 の遅延 Ack が processResult まで届いたことを確認してから resume を
	// 発生させる（そうしないと removeSent(2) が resume 前に完了している保証がない）。
	for {
		ack := <-hooker.afterReceivedAckCh
		if ack.Sequence == 2 {
			break
		}
	}

	// 1 つ目のトランスポートを切断し、resume を発生させる。
	ds[0].Close()

	assert.Eventually(t, func() bool {
		capture.Lock()
		defer capture.Unlock()
		return len(capture.upstreamResumedEvents) > 0
	}, 10*time.Second, 10*time.Millisecond)

	require.NoError(t, up.Close(ctx))
	require.NoError(t, conn.Close(ctx))

	<-d0Done
	<-d1Done
}

// blockingTransport は、armed が真の間だけ最初の Write を release が閉じられる
// まで無期限にブロックする transport.Transport ラッパー。SendUpstreamChunk が
// 永久にブロックするモックを再現するために使う。
type blockingTransport struct {
	transport.Transport
	armed     atomic.Bool
	triggered chan struct{}
	release   chan struct{}
}

func (w *blockingTransport) Write(b []byte) error {
	if w.armed.CompareAndSwap(true, false) {
		close(w.triggered)
		<-w.release
	}
	return w.Transport.Write(b)
}

// TestUpstream_Close_ReturnsWithinCloseTimeoutWhenSendBlocksForever は、
// SendUpstreamChunk が永久にブロックしても Close が closeTimeout を尊重して
// 返ることを検証する（drain の ctx 非対応によるハングの回帰防止）。
//
// 発火条件: waitToSendAllDataPointsAndReceiveAllAck の u.receivedAck.Wait() は
// ctx を見ない。Broadcast の唯一の発生源が processResult であるため、Ack が
// 届かない状況では誰も起こさず Close が無期限にハングする。
//
// オラクル: closeTimeout + 余裕以内に Close(context.Background()) が返ること。
// 修正前はテスト全体の timeout まで返らないので FAIL する。
func TestUpstream_Close_ReturnsWithinCloseTimeoutWhenSendBlocksForever(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	ctx := context.Background()

	d := newDialer(transport.NegotiationParams{})
	bt := &blockingTransport{Transport: d, triggered: make(chan struct{}), release: make(chan struct{})}
	RegisterDialer(TransportTest, func() transport.Dialer {
		return transport.DialerFunc(func(c transport.DialConfig) (transport.Transport, error) {
			if _, err := d.Dial(c); err != nil {
				return nil, err
			}
			return bt, nil
		})
	})
	defer func() {
		select {
		case <-bt.release:
		default:
			close(bt.release)
		}
	}()

	srvDone := make(chan struct{})
	go func() {
		defer close(srvDone)
		mockConnectRequest(t, d.srv)
		openReq := mustReadIgnorePingPong(t, d.srv).(*message.UpstreamOpenRequest)
		mustWrite(t, d.srv, &message.UpstreamOpenResponse{
			RequestID:             openReq.RequestID,
			AssignedStreamID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			AssignedStreamIDAlias: 1,
			ResultCode:            message.ResultCodeSucceeded,
			ResultString:          "OK",
			DataIDAliases:         map[uint32]*message.DataID{},
		})

		// chunk(seq=1) の送信は Write 層でブロックさせているため届かない。
		// 次に届くのは Close 完了時に送られる UpstreamCloseRequest のはず。
		msg := mustReadIgnorePingPong(t, d.srv)
		closeReq, ok := msg.(*message.UpstreamCloseRequest)
		if !ok {
			// 修正前は Close がハングし続け、テストは既に FAIL 済み（呼び出し元の
			// t.Fatal）。その後の cleanup でブロックが解除されるとブロックされて
			// いた古い chunk 送信がここに届くことがあるが、型不一致で panic
			// させずに安全に終了する。
			return
		}
		mustWrite(t, d.srv, &message.UpstreamCloseResponse{
			RequestID:    closeReq.RequestID,
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "OK",
		})
		// conn.Close が送る Disconnect を読み捨てる。
		mustReadIgnorePingPong(t, d.srv)
	}()

	conn, err := Connect("dummy", TransportTest)
	require.NoError(t, err)

	closeTimeout := 2 * time.Second
	up, err := conn.OpenUpstream(ctx,
		"session_id",
		WithUpstreamQoS(message.QoSReliable),
		WithUpstreamFlushPolicyImmediately(),
		WithUpstreamCloseTimeout(closeTimeout),
	)
	require.NoError(t, err)

	dataID := &message.DataID{Name: "name", Type: "type"}
	dp := &message.DataPoint{ElapsedTime: time.Second, Payload: []byte{1, 2, 3, 4}}

	bt.armed.Store(true)
	require.NoError(t, up.WriteDataPoints(ctx, dataID, dp))

	select {
	case <-bt.triggered:
	case <-time.After(5 * time.Second):
		t.Fatal("injected blocking write did not fire")
	}

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- up.Close(context.Background())
	}()

	select {
	case err := <-closeDone:
		require.NoError(t, err)
	case <-time.After(closeTimeout + 3*time.Second):
		t.Fatal("Close did not return within closeTimeout: drain does not respect ctx")
	}

	require.NoError(t, conn.Close(ctx))
	<-srvDone
}
