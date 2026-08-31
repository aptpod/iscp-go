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

	. "github.com/aptpod/iscp-go/iscp"
	"github.com/aptpod/iscp-go/message"
	"github.com/aptpod/iscp-go/transport"
)

// mockUpstreamOpenForReadLoopsTest は UpstreamOpenRequest に
// AssignedStreamIDAlias=1 で応答する。
func mockUpstreamOpenForReadLoopsTest(t *testing.T, d *dialer) {
	t.Helper()
	openReq := mustReadIgnorePingPong(t, d.srv).(*message.UpstreamOpenRequest)
	mustWrite(t, d.srv, &message.UpstreamOpenResponse{
		RequestID:             openReq.RequestID,
		AssignedStreamID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		AssignedStreamIDAlias: 1,
		ResultCode:            message.ResultCodeSucceeded,
		ResultString:          "OK",
		DataIDAliases:         map[uint32]*message.DataID{},
	})
}

// mockResumeAndCloseForReadLoopsTest は再接続後の UpstreamResumeRequest /
// UpstreamCloseRequest / Disconnect に応答する。
func mockResumeAndCloseForReadLoopsTest(t *testing.T, d *dialer) {
	t.Helper()
	msg := mustReadIgnorePingPong(t, d.srv)
	resumeReq, ok := msg.(*message.UpstreamResumeRequest)
	require.True(t, ok, "%T", msg)
	mustWrite(t, d.srv, &message.UpstreamResumeResponse{
		RequestID:             resumeReq.RequestID,
		AssignedStreamIDAlias: 1,
		ResultCode:            message.ResultCodeSucceeded,
		ResultString:          "OK",
		ExtensionFields:       &message.UpstreamResumeResponseExtensionFields{},
	})
	closeReq := mustReadIgnorePingPong(t, d.srv).(*message.UpstreamCloseRequest)
	mustWrite(t, d.srv, &message.UpstreamCloseResponse{
		RequestID:    closeReq.RequestID,
		ResultCode:   message.ResultCodeSucceeded,
		ResultString: "OK",
	})
	mustReadIgnorePingPong(t, d.srv) // Disconnect
}

// TestUpstream_RunWaitsForReadResultLoopExit は、run() が readResultLoop /
// readAliasLoop の終了を待ってから返ることを検証する。
//
// readResultLoop / readAliasLoop が errgroup の外（readAckLoop からの go
// 起動）だと、run() はこれらの終了を待たずに返り、conn.go の再接続ループは
// すぐ次の resume() を呼ぶ。生き残った readResultLoop の defer
// （upstreamChunkResultChs の全 close + map 再作成）が、resume 後（Reliable
// の再送ループが登録した）live なエントリを close してしまうと、以降その
// seq の Ack が届いても upstreamChunkResultChs にエントリが見つからず、
// 対応する sendChunkAndWaitAck の待ち手に Ack が届かない（QoS Reliable の
// 再送と Ack 処理の整合性が壊れる）。run() が読み取りループの終了まで
// 返らなければ、世代を跨いで defer が走る窓は構造的に存在しない。
//
// 「readResultLoop がまだ終了していないのに他の errgroup メンバが全て終了
// した」状態は、大量の Results を積んでからサーバー側 pipe を閉じることで
// 作る。pipe は FIFO なので、クライアントは送信された ack をすべて読み
// 終えてから EOF（切断）を検知する。teardown（数 ms）に対して
// readResultLoop の消化（数十万件の processResult）は十分長く、run() が
// 返る時点で readResultLoop は確実に消化中となる。
//
// 修正前は run() が消化中の readResultLoop を置き去りにして返るため FAIL、
// 修正後は run() が readResultLoop / readAliasLoop の終了まで待ってから
// 返るため PASS する。
func TestUpstream_RunWaitsForReadResultLoopExit(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

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
		mockConnectRequest(t, d1.srv)
		mockUpstreamOpenForReadLoopsTest(t, d1)
		// プローブ: この ack が ReceiveAckHooker に届いたことをもって
		// run() 一式（runWg.Add 済み）の稼働を確認する。WaitRunDoneForTest
		// を run() の Add(1) より先に呼ぶと runWg.Wait() が即座に返って
		// しまうため、この同期が必要。送信は sendProbe（メイン側の
		// OpenUpstream 完了 = alias 1 の購読登録完了）まで遅らせる。open
		// 応答の直後に送ると、購読登録前に届いた ack が捨てられてプローブ
		// が観測できないことがある。
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
			// 存在しない sequence number。processResult は !ok で早期
			// return するが、1 件ごとに u.mu の取得は行うので消化時間は
			// 稼げる。
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
		// 8 通の直後にサーバー側を閉じる。pipe は FIFO なのでクライアント
		// は 8 通を読み終えてから EOF を検知する（切断が ack を追い越さ
		// ない）。
		_ = d1.srv.Close()
	}()

	// 再接続の ConnectRequest への応答は allowReconnect まで遅らせる。
	// これで切断後の connState が Reconnecting に留まり、run() の
	// waitForReconnecting 相当のメンバが確実に検知して errgroup ctx を
	// キャンセルする（応答が速いと Reconnecting 期間が dial 中だけになり、
	// レベル検知が取り逃がすことがある）。オラクル評価の時点で resume
	// 以降（新セッションの読み取りループ）が始まらないことも保証する。
	allowReconnect := make(chan struct{})
	srv2Done := make(chan struct{})
	go func() {
		defer close(srv2Done)
		<-allowReconnect
		mockConnectRequest(t, d2.srv)
		mockResumeAndCloseForReadLoopsTest(t, d2)
	}()

	ctx := context.Background()
	conn, err := Connect("dummy", TransportTest,
		WithConnNodeID("11111111-1111-1111-1111-111111111111"),
		// デフォルトの PingInterval（10 秒）だと、切断検知が次の Ping 送信
		// タイミングまで遅延し、run() の完了を待つ本テストの実行時間が
		// 不安定になる。短くして切断検知を速める。
		WithConnPingInterval(time.Millisecond*200),
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

	// プローブ ack の到着 = readResultLoop が 1 件処理した = run()（1 回目）
	// は既に実行中。ここより前に WaitRunDoneForTest を呼んではいけない
	// （呼び出し時点の runDoneCh のスナップショットを返す仕様のため）。
	close(sendProbe)
	select {
	case <-hooker.afterReceivedAckCh:
	case <-time.After(5 * time.Second):
		t.Fatal("probe ack did not arrive")
	}
	runDone := up.WaitRunDoneForTest()

	// 大量 Results の投入 → サーバー側 close（切断）を進める。
	close(sendGarbage)

	// run() の終了を待つ。readResultLoop / readAliasLoop 以外のメンバ
	// （flushLoop / readAckLoop / waitForReconnecting 相当）は切断の
	// teardown で数 ms で終了する。
	select {
	case <-runDone:
	case <-time.After(10 * time.Second):
		t.Fatal("run() did not return after disconnect")
	}

	// オラクル: run() が返った時点で readResultLoop は終了していなければ
	// ならない。errgroup の外にいると、run() は消化中の readResultLoop を
	// 置き去りにして返り、その defer が resume 後の世代の live な
	// upstreamChunkResultChs エントリを close しうる（Ack 処理の整合性が
	// 壊れる窓）。
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
