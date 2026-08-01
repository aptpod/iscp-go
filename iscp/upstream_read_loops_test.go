package iscp_test

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/aptpod/iscp-go/v2/iscp"
	. "github.com/aptpod/iscp-go/v2/iscp"
	"github.com/aptpod/iscp-go/v2/message"
	"github.com/aptpod/iscp-go/v2/transport"
)

// mockUpstreamOpen は UpstreamOpenRequest に AssignedStreamIDAlias=1 で応答する。
func mockUpstreamOpen(t *testing.T, srv *transport.MessageTransport) {
	openReq := mustRead(t, srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamOpenRequest)
	mustWrite(t, srv, &message.UpstreamOpenResponse{
		RequestID:             openReq.RequestID,
		AssignedStreamID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		AssignedStreamIDAlias: 1,
		ResultCode:            message.ResultCodeSucceeded,
		ResultString:          "OK",
		DataIDAliases:         map[uint32]*message.DataID{},
	})
}

// mockResumeAndClose は再接続後の UpstreamResumeRequest / UpstreamCloseRequest /
// Disconnect に応答する。
func mockResumeAndClose(t *testing.T, srv *transport.MessageTransport) {
	msg := mustRead(t, srv, &message.Ping{}, &message.Pong{})
	resumeReq, ok := msg.(*message.UpstreamResumeRequest)
	require.True(t, ok, "%T", msg)
	mustWrite(t, srv, &message.UpstreamResumeResponse{
		RequestID:             resumeReq.RequestID,
		AssignedStreamIDAlias: 1,
		ResultCode:            message.ResultCodeSucceeded,
		ResultString:          "OK",
		ExtensionFields:       &message.UpstreamResumeResponseExtensionFields{},
	})
	closeReq := mustRead(t, srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamCloseRequest)
	mustWrite(t, srv, &message.UpstreamCloseResponse{
		RequestID:    closeReq.RequestID,
		ResultCode:   message.ResultCodeSucceeded,
		ResultString: "OK",
	})
	mustRead(t, srv, &message.Ping{}, &message.Pong{})
}

// F-3（readAckLoop の resCh への裸送信の select 化）には専用の red テストが
// 無い。resCh の満杯が持続する条件は読み手（readResultLoop）の停止であり、
// I-1（resultCh の cap 1 化）適用後の readResultLoop の停止要因は processResult
// の u.mu 待ちだけだが、readAckLoop は resCh へ送信する直前に
// processDataIDAliases で同じ u.mu を取るため、u.mu を保持して readResultLoop
// を止めると readAckLoop も resCh 送信に到達する前に u.mu 側で停止する。
// つまり「resCh 送信でブロックしたまま解けない」状態を決定的に作る外部
// 同期点が存在しない（詳細は readAckLoop の select 化コメントを参照）。
// select 化は、将来 readResultLoop に別の停止要因が入った場合への多層防御
// として入れており、eg.Wait() の完了性は TestUpstream_RunWaitsForReadResultLoopExit
// が検証する。

// TestUpstream_RunWaitsForReadResultLoopExit は、run() が readResultLoop の
// 終了を待ってから返ることを検証する（F-4 の回帰テスト）。
//
// readResultLoop が errgroup の外（go 起動）だと、run() は readResultLoop の
// 終了を待たずに返り、resume() の runWg.Wait() も素通しする。生き残った
// readResultLoop の defer（upstreamChunkResultChs の全 close + map 再作成）が
// 次セッション世代の live なチャネルを close すると、その ack を待っていた
// sendChunkAndWaitAck は closed チャネルから nil を受けて removeSent へ進み、
// ack が来ていない chunk を ack 済みとして QoS Reliable の再送集合から
// 落とす（データ欠損）。run() が readResultLoop の終了まで返らなければ、
// 世代を跨いで defer が走る窓は構造的に存在しない。
//
// 「readResultLoop がまだ終了していないのに他の errgroup メンバが全て終了
// した」状態は、大量の Results を積んでからサーバー側 pipe を閉じることで
// 作る。pipe は FIFO なので、クライアントは 8 通の ack をすべて読み終えて
// から EOF（切断）を検知する。teardown（数 ms）に対して readResultLoop の
// 消化（数十万件の processResult）は十分長く、run() が返る時点で
// readResultLoop は確実に消化中となる。
func TestUpstream_RunWaitsForReadResultLoopExit(t *testing.T) {
	defer goleak.VerifyNone(t)

	d1 := newDialer(transport.NegotiationParams{})
	d2 := newDialer(transport.NegotiationParams{})
	registerTestTransport(t, []*dialer{d1, d2})

	// 1 通あたりの Results 件数。8 通で 40 万件の processResult になり、
	// teardown より十分長い消化時間を作る（存在しない seq なので副作用なし）。
	const resultsPerAck = 50000

	sendProbe := make(chan struct{})
	sendGarbage := make(chan struct{})
	srv1Done := make(chan struct{})
	go func() {
		defer close(srv1Done)
		// v4 ハンドシェイクで keepalive を無効化する（サーバーモックが read
		// しない区間で ping timeout 切断が起きるのを防ぐ）。
		mockConnectRequestV4(t, d1.srv)
		mockUpstreamOpen(t, d1.srv)
		// プローブ: この ack が ReceiveAckHooker に届いたことをもって run()
		// 一式（runWg.Add 済み）の稼働を確認する。WaitRunDoneForTest を run()
		// の Add(1) より先に呼ぶと runWg.Wait() が即座に返ってしまうため、
		// この同期が必要。送信は sendProbe（メイン側の OpenUpstream 完了 =
		// alias 1 の購読登録完了）まで遅らせる。open 応答の直後に送ると、
		// 購読登録前に届いた ack が捨てられてプローブが観測できないことがある。
		<-sendProbe
		mustWrite(t, d1.srv, &message.UpstreamChunkAck{
			StreamIDAlias: 1,
			Results: []*message.UpstreamChunkResult{
				{SequenceNumber: 9998, ResultCode: message.ResultCodeSucceeded, ResultString: "OK"},
			},
			DataIDAliases:   map[uint32]*message.DataID{},
			ExtensionFields: &message.UpstreamChunkAckExtensionFields{},
		})
		<-sendGarbage
		results := make([]*message.UpstreamChunkResult, resultsPerAck)
		for i := range results {
			// 存在しない sequence number。processResult は !ok で早期 return
			// するが、1 件ごとに u.mu の取得は行うので消化時間は稼げる。
			results[i] = &message.UpstreamChunkResult{
				SequenceNumber: 9999, ResultCode: message.ResultCodeSucceeded, ResultString: "OK",
			}
		}
		for i := 0; i < 8; i++ {
			mustWrite(t, d1.srv, &message.UpstreamChunkAck{
				StreamIDAlias:   1,
				Results:         results,
				DataIDAliases:   map[uint32]*message.DataID{},
				ExtensionFields: &message.UpstreamChunkAckExtensionFields{},
			})
		}
		// 8 通の直後にサーバー側を閉じる。pipe は FIFO なのでクライアントは
		// 8 通を読み終えてから EOF を検知する（切断が ack を追い越さない）。
		_ = d1.srv.Close()
	}()

	// 再接続の ConnectRequest への応答は allowReconnect まで遅らせる。これで
	// 切断後の connState が Reconnecting に留まり、run() の waitForReconnecting
	// が確実に検知して errgroup ctx をキャンセルする（応答が速いと Reconnecting
	// 期間が dial 中だけになり、レベル検知が取り逃がすことがある）。オラクル
	// 評価の時点で resume 以降（新セッションの read ループ）が始まらないことも
	// 保証する。
	allowReconnect := make(chan struct{})
	srv2Done := make(chan struct{})
	go func() {
		defer close(srv2Done)
		<-allowReconnect
		mockConnectRequestV4(t, d2.srv)
		mockResumeAndClose(t, d2.srv)
	}()

	ctx := context.Background()
	conn, err := Connect("dummy", TransportTest,
		iscp.WithConnNodeID("11111111-1111-1111-1111-111111111111"),
	)
	require.NoError(t, err)

	hooker := NewCaptureHooker()
	resumedEvCh := make(chan *UpstreamResumedEvent, 1)
	up, err := conn.OpenUpstream(ctx,
		"session_id",
		WithUpstreamQoS(message.QoSUnreliable),
		WithUpstreamFlushPolicyNone(),
		WithUpstreamReceiveAckHooker(hooker),
		WithUpstreamResumedEventHandler(UpstreamResumedEventHandlerFunc(func(ev *UpstreamResumedEvent) {
			select {
			case resumedEvCh <- ev:
			default:
			}
		})),
	)
	require.NoError(t, err)

	// プローブ ack の到着 = readResultLoop が 1 件処理した = run() は
	// runWg.Add(1) 済み。ここより前に WaitRunDoneForTest を呼んではいけない。
	close(sendProbe)
	select {
	case <-hooker.afterReceivedAckCh:
	case <-time.After(5 * time.Second):
		t.Fatal("probe ack did not arrive")
	}
	runDone := up.WaitRunDoneForTest()

	// 大量 Results の投入 → サーバー側 close（切断）を進める。
	close(sendGarbage)

	// run() の終了を待つ。readResultLoop 以外のメンバ（flushLoop /
	// readAckLoop / waitForReconnecting）は切断の teardown で数 ms で終了する。
	select {
	case <-runDone:
	case <-time.After(10 * time.Second):
		t.Fatal("run() did not return after disconnect")
	}

	// オラクル: run() が返った時点で readResultLoop は終了していなければ
	// ならない。errgroup の外にいると、run() は消化中の readResultLoop を
	// 置き去りにして返り、その defer が resume 後の世代の live チャネルを
	// close しうる（データ欠損の窓）。
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	if strings.Contains(string(buf[:n]), ".readResultLoop(") {
		t.Fatal("run() returned while readResultLoop is still running; readResultLoop must be part of the errgroup")
	}

	// 後始末: 再接続を進めて resume の完了まで確認する。
	close(allowReconnect)
	select {
	case <-resumedEvCh:
	case <-time.After(5 * time.Second):
		t.Fatal("resume did not complete")
	}
	require.NoError(t, up.Close(ctx))
	require.NoError(t, conn.Close(ctx))
	<-srv1Done
	<-srv2Done
}
