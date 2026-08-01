package iscp_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	. "github.com/aptpod/iscp-go/v2/iscp"
	"github.com/aptpod/iscp-go/v2/message"
	"github.com/aptpod/iscp-go/v2/transport"
)

var errWriteFailInjected = errors.New("test: write failure injected")

// writeFailTransport は transport.Transport の Write を 1 回だけ失敗させる
// ラッパー。armed を消費した最初の Write がエラーを返し、failed を通知する。
// それ以外の Write は下層へ素通しする。
type writeFailTransport struct {
	transport.Transport
	armed  atomic.Bool
	failed chan struct{}
}

func (w *writeFailTransport) Write(b []byte) error {
	if w.armed.CompareAndSwap(true, false) {
		close(w.failed)
		return errWriteFailInjected
	}
	return w.Transport.Write(b)
}

// TestUpstream_LateAckAfterSendErrorDoesNotBlockMutex は、送信エラーで
// sendChunkAndWaitAck が resultCh の受信を放棄した後に当該 sequence number の
// Ack が届いても、processResult が u.mu を保持したままブロックしないことを
// 検証する（既定構成で踏める本命経路）。
//
// 発火条件: sendChunkAndWaitAck は SendEncodedUpstreamChunk のエラーで
// upstreamChunkResultChs のエントリを残したまま return する。resultCh が
// バッファなしだと、その seq の Ack が後から届いたとき processResult が
// u.mu（writer）を保持したまま ch <- result で止まり、flush / WriteDataPoints /
// Close / readResultLoop（以降の Ack 処理すべて）が連鎖して止まる。
// 「送信は失敗したがサーバーは Ack を返す」状況は、multi transport の部分
// 送信済みエラーや write timeout（バッファ投入後のエラー）で実在する。
//
// オラクル: 遅延 Ack を届けた後に 2 つ目の chunk を書き、その Ack が
// ReceiveAckHooker まで到達すること。修正前は 1 つ目の遅延 Ack で
// readResultLoop が processResult ごとブロックするため、2 つ目の書き込みも
// Ack 処理も進まず、タイムアウトで FAIL する。
func TestUpstream_LateAckAfterSendErrorDoesNotBlockMutex(t *testing.T) {
	defer goleak.VerifyNone(t)

	d := newDialer(transport.NegotiationParams{})
	wft := &writeFailTransport{Transport: d, failed: make(chan struct{})}
	RegisterDialer(TransportTest, func() transport.Dialer {
		return transport.DialerFunc(func(c transport.DialConfig) (transport.Transport, error) {
			if _, err := d.Dial(c); err != nil {
				return nil, err
			}
			return wft, nil
		})
	})

	// サーバー側の進行はフェーズ同期チャネルで駆動する（sleep で待たない）。
	sendGhostAck := make(chan struct{})
	srvDone := make(chan struct{})
	go func() {
		defer close(srvDone)
		// v4 でハンドシェイクして iSCP レベルの Ping/Pong を無効化する
		// （needsPingPong が false になる）。本テストのサーバーモックは
		// フェーズ同期チャネルで待機する間 read しないため、その間に ping が
		// 来ると Pong を返せず PingTimeout（既定 1s）で切断されてしまう。
		// また ping の Write が writeFailTransport の注入エラーを誤って
		// 消費することも防ぐ。
		mockConnectRequestV4(t, d.srv)
		openReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamOpenRequest)
		mustWrite(t, d.srv, &message.UpstreamOpenResponse{
			RequestID:             openReq.RequestID,
			AssignedStreamID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			AssignedStreamIDAlias: 1,
			ResultCode:            message.ResultCodeSucceeded,
			ResultString:          "OK",
			DataIDAliases:         map[uint32]*message.DataID{},
		})

		// chunk(seq=1) は Write 注入エラーで wire に乗らないため読まない。
		// サーバーはプロトコル上いつでも Ack を送れるので、seq=1 の Ack を
		// 送りつける（部分送信済みでサーバー側は受理しているケースの再現）。
		<-sendGhostAck
		mustWrite(t, d.srv, &message.UpstreamChunkAck{
			StreamIDAlias: 1,
			Results: []*message.UpstreamChunkResult{
				{SequenceNumber: 1, ResultCode: message.ResultCodeSucceeded, ResultString: "OK"},
			},
			DataIDAliases:   map[uint32]*message.DataID{},
			ExtensionFields: &message.UpstreamChunkAckExtensionFields{},
		})

		// 2 つ目の chunk(seq=2) は正常に wire に乗るので読んで Ack を返す。
		chunk := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamChunk)
		mustWrite(t, d.srv, &message.UpstreamChunkAck{
			StreamIDAlias: 1,
			Results: []*message.UpstreamChunkResult{
				{SequenceNumber: chunk.StreamChunk.SequenceNumber, ResultCode: message.ResultCodeSucceeded, ResultString: "OK"},
			},
			DataIDAliases:   map[uint32]*message.DataID{},
			ExtensionFields: &message.UpstreamChunkAckExtensionFields{},
		})

		closeReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamCloseRequest)
		mustWrite(t, d.srv, &message.UpstreamCloseResponse{
			RequestID:    closeReq.RequestID,
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "OK",
		})
		mustRead(t, d.srv, &message.Ping{}, &message.Pong{})
	}()

	ctx := context.Background()
	conn, err := Connect("dummy", TransportTest)
	require.NoError(t, err)

	hooker := NewCaptureHooker()
	up, err := conn.OpenUpstream(ctx,
		"session_id",
		WithUpstreamQoS(message.QoSUnreliable),
		WithUpstreamFlushPolicyNone(),
		WithUpstreamReceiveAckHooker(hooker),
	)
	require.NoError(t, err)

	dataID := &message.DataID{Name: "name", Type: "type"}
	dp := &message.DataPoint{ElapsedTime: time.Second, Payload: []byte{1, 2, 3, 4}}

	// 1 つ目の chunk: Write を注入エラーで失敗させる。sendChunkAndWaitAck は
	// resultCh を受信せずに return し、upstreamChunkResultChs[1] が残る。
	wft.armed.Store(true)
	require.NoError(t, up.WriteDataPoints(ctx, dataID, dp))
	require.NoError(t, up.Flush(ctx))
	select {
	case <-wft.failed:
	case <-time.After(5 * time.Second):
		t.Fatal("injected write failure did not fire")
	}

	// 読み手を失った seq=1 の Ack を届けさせる。
	close(sendGhostAck)

	// オラクル: 2 つ目の chunk の書き込みと Ack 処理が進むこと。修正前は
	// processResult が u.mu を保持したまま ch <- result で止まっているため、
	// WriteDataPoints / flush が u.mu 待ちになり、ここが進まない。
	// require.* は t.FailNow（runtime.Goexit）を呼ぶためテスト goroutine 以外
	// から使えない。エラーはチャネルで本体へ渡して本体側で落とす。
	oracleErr := make(chan error, 1)
	oracleDone := make(chan struct{})
	go func() {
		defer close(oracleDone)
		if err := up.WriteDataPoints(ctx, dataID, dp); err != nil {
			oracleErr <- err
			return
		}
		if err := up.Flush(ctx); err != nil {
			oracleErr <- err
			return
		}
		for {
			ack := <-hooker.afterReceivedAckCh
			if ack.Sequence == 2 {
				return
			}
		}
	}()
	select {
	case <-oracleDone:
		select {
		case err := <-oracleErr:
			t.Fatalf("oracle goroutine failed: %v", err)
		default:
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second chunk was not processed: processResult is blocking with u.mu held")
	}

	require.NoError(t, up.Close(ctx))
	require.NoError(t, conn.Close(ctx))
	<-srvDone
}

// TestUpstream_LateAckAfterAckTimeoutDoesNotBlockMutex は、Ack タイムアウト
// （WithUpstreamAckTimeout、opt-in）で sendChunkAndWaitAck が resultCh の受信を
// 放棄した後に当該 sequence number の Ack が遅れて届いても、processResult が
// u.mu を保持したままブロックしないことを検証する。
//
// オラクルは TestUpstream_LateAckAfterSendErrorDoesNotBlockMutex と同じ。
// こちらは chunk(seq=1) が wire に乗る（サーバーが Ack を遅らせるだけ）点が
// 異なる。タイムアウト発火は外部から観測できないため、AckTimeout(10ms) の
// 発火を十分な余裕（300ms）の sleep で保証する。この sleep は「イベントが
// 起きたことを保証する下限待ち」であり、上限アサーションではないので
// flaky にはならない。
func TestUpstream_LateAckAfterAckTimeoutDoesNotBlockMutex(t *testing.T) {
	defer goleak.VerifyNone(t)

	d := newDialer(transport.NegotiationParams{})
	RegisterDialer(TransportTest, func() transport.Dialer { return d })

	sendLateAck := make(chan struct{})
	srvDone := make(chan struct{})
	go func() {
		defer close(srvDone)
		// v4 ハンドシェイクで Ping/Pong を無効化する理由は
		// TestUpstream_LateAckAfterSendErrorDoesNotBlockMutex と同じ。
		mockConnectRequestV4(t, d.srv)
		openReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamOpenRequest)
		mustWrite(t, d.srv, &message.UpstreamOpenResponse{
			RequestID:             openReq.RequestID,
			AssignedStreamID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			AssignedStreamIDAlias: 1,
			ResultCode:            message.ResultCodeSucceeded,
			ResultString:          "OK",
			DataIDAliases:         map[uint32]*message.DataID{},
		})

		// chunk(seq=1) は読むが、Ack はタイムアウト発火後まで送らない。
		first := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamChunk)
		<-sendLateAck
		mustWrite(t, d.srv, &message.UpstreamChunkAck{
			StreamIDAlias: 1,
			Results: []*message.UpstreamChunkResult{
				{SequenceNumber: first.StreamChunk.SequenceNumber, ResultCode: message.ResultCodeSucceeded, ResultString: "OK"},
			},
			DataIDAliases:   map[uint32]*message.DataID{},
			ExtensionFields: &message.UpstreamChunkAckExtensionFields{},
		})

		second := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamChunk)
		mustWrite(t, d.srv, &message.UpstreamChunkAck{
			StreamIDAlias: 1,
			Results: []*message.UpstreamChunkResult{
				{SequenceNumber: second.StreamChunk.SequenceNumber, ResultCode: message.ResultCodeSucceeded, ResultString: "OK"},
			},
			DataIDAliases:   map[uint32]*message.DataID{},
			ExtensionFields: &message.UpstreamChunkAckExtensionFields{},
		})

		closeReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamCloseRequest)
		mustWrite(t, d.srv, &message.UpstreamCloseResponse{
			RequestID:    closeReq.RequestID,
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "OK",
		})
		mustRead(t, d.srv, &message.Ping{}, &message.Pong{})
	}()

	ctx := context.Background()
	conn, err := Connect("dummy", TransportTest)
	require.NoError(t, err)

	hooker := NewCaptureHooker()
	up, err := conn.OpenUpstream(ctx,
		"session_id",
		WithUpstreamQoS(message.QoSUnreliable),
		WithUpstreamFlushPolicyNone(),
		WithUpstreamAckTimeout(10*time.Millisecond),
		WithUpstreamReceiveAckHooker(hooker),
	)
	require.NoError(t, err)

	dataID := &message.DataID{Name: "name", Type: "type"}
	dp := &message.DataPoint{ElapsedTime: time.Second, Payload: []byte{1, 2, 3, 4}}

	require.NoError(t, up.WriteDataPoints(ctx, dataID, dp))
	require.NoError(t, up.Flush(ctx))

	// AckTimeout(10ms) の発火を保証してから、遅延 Ack を届けさせる。
	time.Sleep(300 * time.Millisecond)
	close(sendLateAck)

	// require.* は t.FailNow（runtime.Goexit）を呼ぶためテスト goroutine 以外
	// から使えない。エラーはチャネルで本体へ渡して本体側で落とす。
	oracleErr := make(chan error, 1)
	oracleDone := make(chan struct{})
	go func() {
		defer close(oracleDone)
		if err := up.WriteDataPoints(ctx, dataID, dp); err != nil {
			oracleErr <- err
			return
		}
		if err := up.Flush(ctx); err != nil {
			oracleErr <- err
			return
		}
		for {
			ack := <-hooker.afterReceivedAckCh
			if ack.Sequence == 2 {
				return
			}
		}
	}()
	select {
	case <-oracleDone:
		select {
		case err := <-oracleErr:
			t.Fatalf("oracle goroutine failed: %v", err)
		default:
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second chunk was not processed: processResult is blocking with u.mu held")
	}

	require.NoError(t, up.Close(ctx))
	require.NoError(t, conn.Close(ctx))
	<-srvDone
}
