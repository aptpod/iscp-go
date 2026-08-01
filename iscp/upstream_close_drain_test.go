package iscp_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	. "github.com/aptpod/iscp-go/v2/iscp"
	"github.com/aptpod/iscp-go/v2/message"
	"github.com/aptpod/iscp-go/v2/transport"
)

// TestUpstream_Close_LateAckAfterAckTimeoutDoesNotBurnCloseTimeout は、Ack
// タイムアウトで待ち手が離脱した chunk の Ack が遅れて届いた場合に、その後の
// Close が closeTimeout を使い切らないことを検証する（B-1 の回帰テスト）。
//
// sentBuf（未 ack chunk の集合）からの削除が sendChunkAndWaitAck（待ち手）
// でしか行われないと、待ち手がタイムアウトで離脱した後に届いた Ack を誰も
// sentBuf に反映しない。すると Close の drain（listSent() が空になるまで
// 待つ）は充足不能になり、サーバーが正しく Ack を返しているのに Close が
// 毎回 closeDeadline を使い切り、in-flight chunk が無いのに cutoff の警告が
// 鳴る。削除の駆動を「Ack の到着」（processResult）に移すことで、待ち手の
// 生死に関係なく sentBuf が Ack を反映するようになる。
func TestUpstream_Close_LateAckAfterAckTimeoutDoesNotBurnCloseTimeout(t *testing.T) {
	defer goleak.VerifyNone(t)

	d := newDialer(transport.NegotiationParams{})
	RegisterDialer(TransportTest, func() transport.Dialer { return d })

	sendLateAck := make(chan struct{})
	srvDone := make(chan struct{})
	go func() {
		defer close(srvDone)
		// v4 ハンドシェイクで keepalive を無効化する（サーバーモックが read
		// しない区間で ping timeout 切断が起きるのを防ぐ）。
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
		WithUpstreamCloseTimeout(2*time.Second),
		WithUpstreamReceiveAckHooker(hooker),
	)
	require.NoError(t, err)

	dataID := &message.DataID{Name: "name", Type: "type"}
	dp := &message.DataPoint{ElapsedTime: time.Second, Payload: []byte{1, 2, 3, 4}}
	require.NoError(t, up.WriteDataPoints(ctx, dataID, dp))
	require.NoError(t, up.Flush(ctx))

	// AckTimeout(10ms) の発火（待ち手の離脱）を保証してから、遅延 Ack を
	// 届けさせる。この sleep はイベント発生を保証する下限待ちで、上限
	// アサーションではないので flaky にはならない。
	time.Sleep(300 * time.Millisecond)
	close(sendLateAck)

	// 遅延 Ack が処理されたことを確認する（HookAfter は readResultLoop 上で
	// キューイングされるため、到着 = Ack 処理の進行）。
	select {
	case <-hooker.afterReceivedAckCh:
	case <-time.After(5 * time.Second):
		t.Fatal("late ack did not arrive")
	}

	// オラクル: 遅延 Ack が sentBuf に反映されていれば、Close の drain は
	// 即座に充足して Close はすぐ返る。反映されない（修正前）と drain が
	// closeTimeout(2s) を使い切る。
	start := time.Now()
	require.NoError(t, up.Close(ctx))
	assert.Less(t, time.Since(start), 1500*time.Millisecond,
		"Close should not burn closeTimeout waiting for a chunk whose ack already arrived")

	require.NoError(t, conn.Close(ctx))
	<-srvDone
}
