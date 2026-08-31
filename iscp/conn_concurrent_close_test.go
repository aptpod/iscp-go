package iscp_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/aptpod/iscp-go/errors"
	. "github.com/aptpod/iscp-go/iscp"
	"github.com/aptpod/iscp-go/message"
	"github.com/aptpod/iscp-go/transport"
)

// TestConn_Close_TimesOutWhenDisconnectSendBlocks は、SendDisconnect の Write が
// 下層 transport でブロックし続けても、Conn.Close が一定時間内に返ることを保証する
// regression テスト。
//
// SendDisconnect は ctx を無視して transport.Write を呼ぶため、相手がメッセージを
// 一切読まない状況では下層 pipe の Write がブロックし続ける（transport/pipe.go の
// pipe.Write 参照）。close 内で SendDisconnect を別 goroutine + select で待つことで、
// disconnectSendTimeout（既定 3秒）を超えたら wireConn.Close() のみ実行して返る
// 必要がある。
func TestConn_Close_TimesOutWhenDisconnectSendBlocks(t *testing.T) {
	defer goleak.VerifyNone(t)
	d := newDialer(transport.NegotiationParams{})
	RegisterDialer(TransportTest, func() transport.Dialer { return d })

	go func() {
		mockConnectRequest(t, d.srv)
		// 以後は一切 Read しない。Disconnect送信のWriteは下層pipeで
		// 相手が読むまでブロックし続ける。
	}()

	conn, err := Connect("dummy", TransportTest)
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() { done <- conn.Close(context.Background()) }()

	// disconnectSendTimeout（既定3s）+ 余裕を見て5s以内に返ることを期待する。
	select {
	case err := <-done:
		assert.NoError(t, err, "wireConn.Close() の結果がそのまま返るはず")
	case <-time.After(5 * time.Second):
		t.Fatal("Conn.Close did not return within disconnectSendTimeout + margin")
	}
}

// TestConn_Send_AfterClose_ReturnsErrConnectionClosed は、Close済みのConnへ
// メッセージ送信を試みた場合に、永久ブロックせず速やかにErrConnectionClosedが
// 返却されることを検証する。
//
// state.go の WaitUntilOrClosed に渡す hooker は「現在の状態がClosedかどうか」を
// 見て諦める判定である必要があるが、待機目標値（常にconnStatusConnected）を渡すと
// 現在の状態を一切参照しないため、Close後もConnectedになるのを永久に待ち続けて
// しまう（Conn.send のブロック）。
func TestConn_Send_AfterClose_ReturnsErrConnectionClosed(t *testing.T) {
	defer goleak.VerifyNone(t)
	d := newDialer(transport.NegotiationParams{})
	RegisterDialer(TransportTest, func() transport.Dialer { return d })
	done := make(chan struct{})
	go func() {
		defer close(done)
		mockConnectRequest(t, d.srv)
		assert.Equal(t, &message.Disconnect{
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "NormalClosure",
		}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}))
	}()

	conn, err := Connect("dummy", TransportTest)
	require.NoError(t, err)

	require.NoError(t, conn.Close(context.Background()))
	<-done

	resultCh := make(chan error, 1)
	go func() {
		resultCh <- conn.SendBaseTime(context.Background(), &message.BaseTime{
			SessionID: "session_id",
		})
	}()

	select {
	case err := <-resultCh:
		assert.ErrorIs(t, err, errors.ErrConnectionClosed)
	case <-time.After(time.Second):
		t.Fatal("Conn.send did not return after Close: possible permanent block")
	}
}
