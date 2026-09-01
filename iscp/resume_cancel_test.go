package iscp

import (
	"context"
	"testing"
	"time"

	uuid "github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/aptpod/iscp-go/log"
	"github.com/aptpod/iscp-go/message"
	"github.com/aptpod/iscp-go/wire"
)

// resume のリトライを何回まわしてからキャンセルするか。
//
// internal/retry のバックオフは 100ms * 2^retryCount（jitter 0.5〜1.5倍、上限 5 秒）。
// retryCount は 0 始まりなので、6 回応答した直後は 100ms * 2^5 = 3.2 秒に jitter が
// かかった 1.6〜4.8 秒のスリープが目前に控えている。下限が 1 秒を確実に超えるため、
// 「キャンセルがスリープを打ち切れたか」を 1 秒のしきい値で判定できる。
const resumeConflictsBeforeCancel = 6

// serveResumeConflict は、Upstream / Downstream いずれの ResumeRequest に対しても
// ResumeRequestConflict を返し続けるテスト用サーバーループを起動します。
// 応答するたびに answered へ通知します。
//
// resume() の実装は Conflict をリトライ継続の合図として扱うため、このサーバーを
// 相手にすると resume() はバックオフしながらリトライを続ける状態になります。
func serveResumeConflict(srv wire.EncodingTransport, answered chan<- struct{}) {
	notify := func() {
		select {
		case answered <- struct{}{}:
		default:
		}
	}
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
			case *message.DownstreamResumeRequest:
				_ = srv.Write(&message.DownstreamResumeResponse{
					RequestID:       m.RequestID,
					ResultCode:      message.ResultCodeResumeRequestConflict,
					ResultString:    "conflict",
					ExtensionFields: &message.DownstreamResumeResponseExtensionFields{},
				})
				notify()
			case *message.UpstreamResumeRequest:
				_ = srv.Write(&message.UpstreamResumeResponse{
					RequestID:       m.RequestID,
					ResultCode:      message.ResultCodeResumeRequestConflict,
					ResultString:    "conflict",
					ExtensionFields: &message.UpstreamResumeResponseExtensionFields{},
				})
				notify()
			case *message.DownstreamCloseRequest:
				// resume 失敗時の後始末（closeWithError）に応答する。
				_ = srv.Write(&message.DownstreamCloseResponse{
					RequestID:    m.RequestID,
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "OK",
				})
			case *message.UpstreamCloseRequest:
				_ = srv.Write(&message.UpstreamCloseResponse{
					RequestID:    m.RequestID,
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "OK",
				})
			}
		}
	}()
}

// waitResumeConflicts は、サーバーが n 回 Conflict を返すまで待ちます。
func waitResumeConflicts(t *testing.T, answered <-chan struct{}, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-answered:
		case <-time.After(30 * time.Second):
			t.Fatalf("resume のリトライが %d 回目まで到達しなかった", i+1)
		}
	}
}

// TestDownstreamResume_CancelDuringRetry_DoesNotReportSuccess は、Conflict の
// リトライ待ち中に d.ctx がキャンセルされた場合、resume() が「再開成功」として
// 後続へ進まないことを検証します。
//
// retry.DoWithContext はキャンセルを検知すると f を呼ばずに戻るため、Conflict
// 応答の直後にキャンセルされると resErr は nil のままになります。ガードが無いと
// dpsCh / ackCompCh / metaCh を旧世代のまま残したまま streamStatusConnected へ
// 遷移し、Resumed イベントまで発火してしまいます。
//
// なお Downstream の f は毎回 SubscribeDownstreamChunk を呼び直すため、この
// テスト用ハーネスではリトライを 2 周させられません（同一エイリアスの再購読が
// 失敗する）。そのため本テストが押さえるのはガードの契約だけで、
// retry.Do から retry.DoWithContext への変更自体は検出できません。
// 「キャンセルがスリープを即座に打ち切れること」は Upstream 側のテストが担います。
func TestDownstreamResume_CancelDuringRetry_DoesNotReportSuccess(t *testing.T) {
	cliConn, srv := newTestClientConnPair(t)
	answered := make(chan struct{}, 64)
	serveResumeConflict(srv, answered)

	ctx, cancel := context.WithCancel(context.Background())
	d := &Downstream{
		ctx:             ctx,
		cancel:          cancel,
		ID:              uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		wireConn:        cliConn,
		idAlias:         1,
		state:           newStreamState(),
		eventDispatcher: newEventDispatcher(),
		logger:          log.NewNop(),
		Config:          defaultDownstreamConfig,
		closeTimeout:    time.Second,
		finalAckFlushed: make(chan struct{}),
	}
	d.state.Swap(streamStatusResuming)

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.resume(&Conn{wireConn: cliConn})
	}()

	// 1 回目の Conflict 応答を待ち、クライアントがリトライ間隔のスリープに
	// 入ってからキャンセルする。応答直後だと SendDownstreamResumeRequest が
	// in-flight のまま中断されて resErr が入り、ガードの経路へ到達しない。
	//
	// 1 回目のスリープは 100ms * 2^0 に jitter が掛かった 50〜150ms なので、
	// 待ち時間はその下限より確実に短くしておく（長すぎるとスリープが明けて
	// 2 回目の試行に入ってしまい、これも resErr が入ってガードへ届かない）。
	waitResumeConflicts(t, answered, 1)
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		assert.Error(t, err, "再開が成立していない resume は成功を返してはならない")
		assert.NotEqual(t, streamStatusConnected, d.state.Current(),
			"resume が成立していないのに接続済みへ遷移してはならない")
	case <-time.After(30 * time.Second):
		t.Fatal("resume が返らなかった")
	}
}

// TestUpstreamResume_CancelDuringConflictRetry_Aborts は、Downstream 版と同じ検証を
// Upstream の resume に対して行います。
func TestUpstreamResume_CancelDuringConflictRetry_Aborts(t *testing.T) {
	cliConn, srv := newTestClientConnPair(t)
	answered := make(chan struct{}, 64)
	serveResumeConflict(srv, answered)

	u := NewUpstreamForTest(cliConn, uuid.MustParse("44444444-4444-4444-4444-444444444444"), 1, time.Second)
	u.state.Swap(streamStatusResuming)

	errCh := make(chan error, 1)
	go func() {
		errCh <- u.resume(cliConn)
	}()

	waitResumeConflicts(t, answered, resumeConflictsBeforeCancel)

	// サーバーが応答を書いた直後はクライアントがまだ受信処理中で、リトライ間隔の
	// スリープに入っていない。その状態でキャンセルすると送信中の RPC が中断されて
	// 返るだけになり、「スリープを打ち切れたか」を判定できない。少し待ってから
	// キャンセルする。
	time.Sleep(100 * time.Millisecond)

	canceledAt := time.Now()
	u.cancel()

	select {
	case err := <-errCh:
		elapsed := time.Since(canceledAt)
		assert.Error(t, err, "ctx キャンセルで打ち切られた resume は成功を返してはならない")
		assert.Less(t, elapsed, time.Second,
			"ctx キャンセルはリトライ間隔のスリープを即座に打ち切らなければならない（実測 %v）", elapsed)
	case <-time.After(30 * time.Second):
		t.Fatal("resume が ctx キャンセル後も返らなかった")
	}
}
