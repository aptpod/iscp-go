package multi_test

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aptpod/iscp-go/v2/log"
	"github.com/aptpod/iscp-go/v2/transport"
	. "github.com/aptpod/iscp-go/v2/transport/multi"
	"github.com/aptpod/iscp-go/v2/transport/reconnect"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// newExhaustingAfterConnectTransport は「初回接続のみ成功し、以後の再接続は常に失敗する」
// reconnect.Transport を作る。doReconnect のリトライを枯渇させて StatusDisconnected へ落とす。
//
// **初回接続から失敗させてはいけない。** initialConnect の枯渇経路は doneProcess の中で
// r.cancel() を呼ぶ（transport/reconnect/transport.go:241-250, :253-256）ので goroutine が
// 自然に回収されてしまい、P1 を再現できない。P1 が起きるのは、一度 Connected になった後に
// doReconnect のリトライが枯渇する経路（:775-780）だけ。
//
// 2026-07-31 実測: 当初は「初回接続から常に失敗」させる版で書いたが、それでは
// initialConnect の枯渇経路（doneProcess 内で r.cancel() を呼ぶ）を通ってしまい goroutine が
// 自然に回収され、テストが PASS してしまって P1 を再現できなかった。この版（初回のみ成功）に
// 差し替えて初めて FAIL することを確認した。
//
// transportType が空文字のときは v4 プロトコル機能が無効になり heartbeatLoop が起動しない
// （transport/reconnect/transport.go:199,227-229）。v4 無効ではそもそも leak しないので、
// 実質的に P1 を検出するのは v4 有効のケース。両方書くのは回帰防止のため。
func newExhaustingAfterConnectTransport(t *testing.T, mock *mockTransport, subConnID string, transportType transport.Name) *reconnect.Transport {
	t.Helper()
	var dialCount atomic.Int32
	rt, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			if dialCount.Add(1) == 1 {
				return mock, nil // 初回のみ成功
			}
			return nil, errors.New("test: reconnect always fails")
		}),
		DialConfig: transport.DialConfig{
			SubConnectionID:   transport.SubConnectionID(subConnID),
			SuperConnectionID: transport.SuperConnectionID(testSuperConnectionID),
			TransportType:     transportType,
		},
		MaxReconnectAttempts: 1, // 再接続 1 回で枯渇させる
		ReconnectInterval:    10 * time.Millisecond,
		HeartbeatInterval:    time.Hour,
		HeartbeatTimeout:     time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)
	return rt
}

// TestMultiTransport_全sub枯渇後にsubのgoroutineが残らない は、全 sub-connection が
// 有限リトライを使い切って StatusDisconnected に到達したあと、multi.Transport が
// sub を Close して goroutine を回収することを検証する。
//
// 修正前は transport.go:330-333 が m.cancel() だけを呼んでいた。reconnect.Transport の
// ctx は context.Background() 由来で m.ctx と親子関係が無いため（reconnect/transport.go:224）
// cancel は sub へ伝播せず、v4 有効時は heartbeatLoop が残っていた（doReconnect は
// リトライ枯渇時にそれ自身が return するため dial は継続せず、readLoop も残らない）。
func TestMultiTransport_全sub枯渇後にsubのgoroutineが残らない(t *testing.T) {
	for _, tt := range []struct {
		name          string
		transportType transport.Name
	}{
		{name: "v4無効", transportType: ""},
		{name: "v4有効", transportType: transport.NegotiationNameWebSocket},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// 検査の後に後片付けする。t.Cleanup は関数内の defer が全て走った
			// 後に実行されるため、この順序でしか「明示 Close なしで回収されるか」を
			// 検証できない（defer で Close すると leak が消えてテストが無意味になる）。
			defer goleak.VerifyNone(t)

			mock1 := newMockTransport("mock1")
			mock2 := newMockTransport("mock2")
			rt1 := newExhaustingAfterConnectTransport(t, mock1, "sub1", tt.transportType)
			rt2 := newExhaustingAfterConnectTransport(t, mock2, "sub2", tt.transportType)

			// 一度 Connected にしてから切断させる（そうしないと initialConnect 経路を
			// 通ってしまい P1 を再現できない。ヘルパーのコメント参照）。
			waitForConnected(t, rt1)
			waitForConnected(t, rt2)

			id1 := transport.SubConnectionID("transport1")
			id2 := transport.SubConnectionID("transport2")

			mt, err := NewTransport(TransportConfig{
				TransportMap: map[transport.SubConnectionID]*reconnect.Transport{
					id1: rt1,
					id2: rt2,
				},
				TransportSelector: newFixedTransportSelector(id1),
				Logger:            log.NewNop(),
				// 親の閾値では畳まない。sub 自身のリトライ枯渇だけで
				// MultiOverallStatusDisconnected へ到達させ、P1 の経路を通す。
				NoConnectedTransportTimeout: 0,
			})
			require.NoError(t, err)
			t.Cleanup(func() { closeMultiAndWait(mt) })

			// multi へ登録してから切断させる。以後の再接続は必ず失敗するので
			// MaxReconnectAttempts=1 が枯渇し、sub は StatusDisconnected へ落ちる。
			mock1.Close()
			mock2.Close()

			require.Eventually(t, func() bool {
				return mt.OverallStatus() == MultiOverallStatusDisconnected
			}, 3*time.Second, 10*time.Millisecond,
				"全 sub がリトライを枯渇したら OverallStatus は Disconnected になるはず")

			// teardown は status callback とは別 goroutine で走るので少し待つ。
			// 明示 Close は呼ばない（呼ぶと P1 の検証にならない）。
			time.Sleep(300 * time.Millisecond)
		})
	}
}
