package iscp

import (
	"context"
	"sync"
	"testing"
	"time"

	uuid "github.com/google/uuid"

	"github.com/aptpod/iscp-go/log"
	"github.com/aptpod/iscp-go/message"
	"github.com/aptpod/iscp-go/wire"
)

// serveDownstreamResumeAndCloseは、DownstreamのCloseRequestとResumeRequestの
// どちらが届いても成功応答を返すテスト用サーバーループを起動します。
//
// resume()とClose()を意図的にロックなしで競合させるテストでは、d.wireConnの
// 読みがどちらの接続（再開前/再開後）を掴むかは実行毎に変わり得るため、
// 両方の接続で両方のリクエスト種別に応答できる必要があります。
func serveDownstreamResumeAndClose(srv wire.EncodingTransport) {
	go func() {
		for {
			msg, err := srv.Read()
			if err != nil {
				return
			}
			switch m := msg.(type) {
			case *message.Ping:
				_ = srv.Write(&message.Pong{
					RequestID:       m.RequestID,
					ExtensionFields: &message.PongExtensionFields{},
				})
			case *message.DownstreamCloseRequest:
				_ = srv.Write(&message.DownstreamCloseResponse{
					RequestID:    m.RequestID,
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "OK",
				})
			case *message.DownstreamResumeRequest:
				_ = srv.Write(&message.DownstreamResumeResponse{
					RequestID:       m.RequestID,
					ResultCode:      message.ResultCodeSucceeded,
					ResultString:    "OK",
					ExtensionFields: &message.DownstreamResumeResponseExtensionFields{},
				})
			}
		}
	}()
}

// TestDownstreamResumeRaceWithCloseは、再開(resume)によるDownstream.wireConnの
// 差し替えと、利用者goroutineからのClose呼び出しによる読みを並行実行しても
// データレースが起きないことを検証します（-raceで検出）。
//
// RED理由: 修正前はresume()内の`d.wireConn = parentConn.wireConn`と
// `parentConn.wireConn`の読みがd.mu/c.wireConnMuの外で行われ、Close()
// （closeWithError）のd.wireConn読みもロックの外でした。再開経路と
// Close呼び出し元goroutineが並行実行されると、-raceがDATA RACEを検出します。
//
// resume()はd.state.Is(streamStatusResuming)を確認してからd.wireConnへ書き込む
// が、Close()はSwap(streamStatusDraining)で先にその状態を潰してしまうことがある。
// この場合resume()は書き込み前にreturnしてしまい、レースの検出機会を逃す
// （実測で約半数の実行がこのケースに当たる）。検出力を上げるため、connを
// 使い回しながらテスト本体を100回ループする。
func TestDownstreamResumeRaceWithClose(t *testing.T) {
	oldConn, oldSrv := newTestClientConnPair(t)
	newConn, newSrv := newTestClientConnPair(t)
	serveDownstreamResumeAndClose(oldSrv)
	serveDownstreamResumeAndClose(newSrv)

	parentConn := &Conn{wireConn: newConn}

	const iterations = 100
	for i := 0; i < iterations; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		d := &Downstream{
			ctx:             ctx,
			cancel:          cancel,
			ID:              uuid.MustParse("22222222-2222-2222-2222-222222222222"),
			wireConn:        oldConn,
			idAlias:         1,
			state:           newStreamState(),
			eventDispatcher: newEventDispatcher(),
			logger:          log.NewNop(),
			Config:          defaultDownstreamConfig,
			closeTimeout:    time.Second,
			finalAckFlushed: make(chan struct{}),
		}
		d.state.Swap(streamStatusResuming)

		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_ = d.resume(parentConn)
		}()
		go func() {
			defer wg.Done()
			<-start
			_ = d.Close(context.Background())
		}()
		close(start)

		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("resume/Close did not complete within the expected time (iteration %d)", i)
		}
	}
}
