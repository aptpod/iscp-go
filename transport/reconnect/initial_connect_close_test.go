package reconnect_test

import (
	"testing"
	"time"

	"github.com/aptpod/iscp-go/v2/log"
	"github.com/aptpod/iscp-go/v2/transport"
	. "github.com/aptpod/iscp-go/v2/transport/reconnect"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// TestReconnectTransport_初期接続中のCloseでもdialした下層transportが閉じられる は、
// initialConnect と CloseWithStatus の競合で下層トランスポートがリークしないことを
// 検証する regression テスト。
//
// 競合の経路（修正前のバグ）:
//
//  1. initialConnect は closed チェックを dialer.Dial の前でしか行わない
//  2. チェック通過後（dialer.Dial 実行中）に CloseWithStatus が完走すると、
//     r.transport == nil を見て何も閉じずに Close が完了する
//     （waitForReconnectToFinish は reconnectMu しか待たず、initialConnect は
//     reconnectMu を取らないため、待ち合わせをすり抜ける）
//  3. その後 initialConnect が r.transport をセットし Status を Connected に戻すが、
//     このトランスポートを閉じる主体はもう存在しない → 恒久リーク
//
// 実際には transport/multi パッケージの goleak flaky として観測された
// （give_up_defaults_test.go の TestCalcNoConnectedTransportTimeout_MatchesReconnectDefaults
// が Dial 直後に接続完了を待たず Close する経路で、全パッケージ並列実行の高負荷時のみ
// この窓を踏み、readLoop + 内部リーダー goroutine が永久残留していた）。
//
// 本テストは dialer を「Close の完了までブロック」させることで、確率に頼らず
// この窓を 100% 決定的に踏ませる。
func TestReconnectTransport_初期接続中のCloseでもdialした下層transportが閉じられる(t *testing.T) {
	defer goleak.VerifyNone(t)

	mock := newMockCountingTransport("mock1")
	dialEntered := make(chan struct{})
	closeDone := make(chan struct{})
	tr, err := Dial(DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			// initialConnect が closed チェックを通過して dial に入ったことを
			// テスト本体へ通知した上で、Close の完走までブロックする。
			// （dial 到達前に Close されると initialConnect はループ先頭の closed
			// チェックで打ち切られ、競合窓自体を踏まないため、この同期が要る）
			close(dialEntered)
			<-closeDone
			return mock, nil
		}),
		DialConfig: transport.DialConfig{
			SubConnectionID: "sub1",
		},
		MaxReconnectAttempts: 1,
		ReconnectInterval:    10 * time.Millisecond,
		HeartbeatInterval:    time.Hour,
		HeartbeatTimeout:     time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)

	// initialConnect が dialer.Dial に入る（= closed チェック通過済み）まで待つ。
	select {
	case <-dialEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("initialConnect did not reach dialer.Dial within 5s")
	}

	// initialConnect は dialer 内でブロック中。この Close は r.transport == nil を
	// 見て「閉じる対象なし」で完了する。
	require.NoError(t, tr.Close())

	// Close 完了後に dialer を解放する。initialConnect はこの後 transport を
	// 受け取るが、Close 済みであることを検知して自ら閉じなければならない。
	close(closeDone)

	require.Eventually(t,
		func() bool { return mock.CloseCount() >= 1 },
		time.Second, 5*time.Millisecond,
		"Close 完了後に initialConnect が dial した下層 transport は閉じられるはず")
	require.Equal(t, StatusDisconnected, tr.Status(),
		"Close 後に Status が Connected へ戻ってはいけない")
}
