package multi_test

import (
	"context"
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

// fixedTransportSelector は常に固定の SubConnectionID を返すセレクタ。
// mockTransportSelector と異なり MultiTransportSetter を実装しないため、
// SelectAvailableTransport による status-aware フォールバックを迂回できる。
// これにより、selector が選んだ sub-conn 自体が Connecting/Reconnecting で
// あるケースを意図的に作り、multi.Transport.Write 自身のフォールバックパスを検証できる。
type fixedTransportSelector struct {
	selected transport.SubConnectionID
}

func newFixedTransportSelector(selected transport.SubConnectionID) *fixedTransportSelector {
	return &fixedTransportSelector{selected: selected}
}

func (s *fixedTransportSelector) Get(_ context.Context, _ int64) transport.SubConnectionID {
	return s.selected
}

// TestReconnectTransport_Write_DoesNotBlockOnUnlimitedRetry は、
// MaxReconnectAttempts=-1 でダイアラーが永久ブロックする状況下でも
// reconnect.Transport.Write() が速やかにエラーを返すことを保証する regression テスト。
func TestReconnectTransport_Write_DoesNotBlockOnUnlimitedRetry(t *testing.T) {
	blockDial := make(chan struct{})
	var dialCount atomic.Int32
	mock := newMockTransport("mock1")

	rt, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			if dialCount.Add(1) == 1 {
				return mock, nil
			}
			<-blockDial
			return nil, errors.New("test: dialer canceled")
		}),
		DialConfig: transport.DialConfig{
			SubConnectionID:   "sub1",
			SuperConnectionID: transport.SuperConnectionID(testSuperConnectionID),
			TransportType:     transport.NegotiationNameWebSocket,
		},
		MaxReconnectAttempts: -1,
		ReconnectInterval:    10 * time.Millisecond,
		HeartbeatInterval:    time.Hour,
		HeartbeatTimeout:     time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		close(blockDial)
		_ = rt.Close()
	})

	require.Eventually(t,
		func() bool { return rt.Status() == reconnect.StatusConnected },
		5*time.Second, 10*time.Millisecond,
	)

	mock.Close()
	require.Eventually(t,
		func() bool { return rt.Status() == reconnect.StatusReconnecting },
		5*time.Second, 10*time.Millisecond,
	)

	done := make(chan error, 1)
	go func() { done <- rt.Write([]byte("test data")) }()

	select {
	case err := <-done:
		require.Error(t, err, "Write should return an error during unlimited-retry reconnect")
	case <-time.After(2 * time.Second):
		t.Fatal("Write blocked >2s during unlimited-retry reconnect")
	}
}

// TestMultiTransport_Write_FallbackDuringUnlimitedRetry は、
// 複数 sub-conn のうち 1 つが MaxReconnectAttempts=-1 でリトライ中・宛先ブロック状態でも
// multi.Transport.Write() が他の健全な sub-conn へフォールバックして無限ブロックしないことを
// 保証する regression テスト。
func TestMultiTransport_Write_FallbackDuringUnlimitedRetry(t *testing.T) {
	blockDial1 := make(chan struct{})
	var dialCount1 atomic.Int32
	mock1 := newMockTransport("mock1")

	rt1, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			if dialCount1.Add(1) == 1 {
				return mock1, nil
			}
			<-blockDial1
			return nil, errors.New("test: dialer canceled")
		}),
		DialConfig: transport.DialConfig{
			SubConnectionID:   "sub1",
			SuperConnectionID: transport.SuperConnectionID(testSuperConnectionID),
			TransportType:     transport.NegotiationNameWebSocket,
		},
		MaxReconnectAttempts: -1,
		ReconnectInterval:    10 * time.Millisecond,
		HeartbeatInterval:    time.Hour,
		HeartbeatTimeout:     time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)

	mock2 := newMockTransport("mock2")
	rt2 := newTestReconnectTransport(t, mock2, "sub2")

	waitForConnected(t, rt1)
	waitForConnected(t, rt2)

	id1 := transport.SubConnectionID("transport1")
	id2 := transport.SubConnectionID("transport2")
	selector := newMockTransportSelector(id1)

	mt, err := NewTransport(TransportConfig{
		TransportMap: TransportMap{
			id1: rt1,
			id2: rt2,
		},
		TransportSelector:   selector,
		Logger:              log.NewNop(),
		StatusCheckInterval: 100 * time.Millisecond,
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		close(blockDial1)
		_ = mt.Close()
		time.Sleep(200 * time.Millisecond)
	})

	// sub1 を Reconnecting 状態で固めた上で multi.Write をかけると、
	// SelectAvailableTransport が sub2 を返すため mock2 に書き込まれる。
	mock1.Close()
	require.Eventually(t,
		func() bool { return rt1.Status() == reconnect.StatusReconnecting },
		5*time.Second, 10*time.Millisecond,
	)

	done := make(chan error, 1)
	go func() { done <- mt.Write([]byte("multi fallback data")) }()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("multi.Transport.Write blocked >2s despite healthy sub2 available")
	}

	select {
	case received := <-mock2.writeCh:
		require.NotEmpty(t, received)
	case <-time.After(time.Second):
		t.Fatal("write did not reach sub2 within 1s")
	}
}

// TestReconnectTransport_Write_FailsAfterFiniteRetriesExhausted は、
// 有限 MaxReconnectAttempts で再接続が全て失敗した後に Write() が
// 永久ポーリングせず速やかにエラーを返すことを保証する regression テスト。
// （Status が Reconnecting のまま固定されると waitForWritable が固まるバグ対策）
func TestReconnectTransport_Write_FailsAfterFiniteRetriesExhausted(t *testing.T) {
	var dialCount atomic.Int32
	mock := newMockTransport("mock1")

	rt, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			if dialCount.Add(1) == 1 {
				return mock, nil
			}
			return nil, errors.New("test: dialer always fails on reconnect")
		}),
		DialConfig: transport.DialConfig{
			SubConnectionID:   "sub1",
			SuperConnectionID: transport.SuperConnectionID(testSuperConnectionID),
			TransportType:     transport.NegotiationNameWebSocket,
		},
		MaxReconnectAttempts: 2,
		ReconnectInterval:    10 * time.Millisecond,
		HeartbeatInterval:    time.Hour,
		HeartbeatTimeout:     time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })

	require.Eventually(t,
		func() bool { return rt.Status() == reconnect.StatusConnected },
		5*time.Second, 10*time.Millisecond,
	)

	mock.Close()
	require.Eventually(t,
		func() bool { return rt.Status() == reconnect.StatusDisconnected },
		5*time.Second, 10*time.Millisecond,
		"status should transition to Disconnected after retries are exhausted",
	)

	done := make(chan error, 1)
	go func() { done <- rt.Write([]byte("data")) }()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Write blocked >2s after reconnect retries were exhausted")
	}
}

// TestMultiTransport_Write_DoesNotFallbackOnPartialSendError は、
// sub-conn の下層 Write が部分送信後にエラーを返した場合に、
// multi.Transport が同じペイロードを別 sub-conn に再送しない（重複/破損防止）ことを
// 保証する regression テスト。
func TestMultiTransport_Write_DoesNotFallbackOnPartialSendError(t *testing.T) {
	mock1 := newMockTransport("mock1")
	rt1 := newTestReconnectTransport(t, mock1, "sub1")
	mock2 := newMockTransport("mock2")
	rt2 := newTestReconnectTransport(t, mock2, "sub2")
	waitForConnected(t, rt1)
	waitForConnected(t, rt2)

	id1 := transport.SubConnectionID("transport1")
	id2 := transport.SubConnectionID("transport2")
	selector := newMockTransportSelector(id1)

	mt, err := NewTransport(TransportConfig{
		TransportMap: TransportMap{
			id1: rt1,
			id2: rt2,
		},
		TransportSelector:   selector,
		Logger:              log.NewNop(),
		StatusCheckInterval: 100 * time.Millisecond,
	})
	require.NoError(t, err)
	t.Cleanup(func() { closeAndWait(t, mt) })

	// mock1 を close することで sub1 の reconnect.Transport 内の下層 tr.Write は
	// 「transport closed」エラーを返す（mock の Write は isClosed で error を返す仕様）。
	// これは ErrNotConnected ではない（部分送信相当の扱い）。
	mock1.Close()

	// 直後（Status が StatusConnected のうちに）Write を発行すると、
	// waitForWritable は Connected を見て tr.Write を呼び、その結果エラーが返る。
	// このエラーは ErrNotConnected で包まれていないため、multi は sub2 にフォールバックしない。
	err = mt.Write([]byte("payload"))
	require.Error(t, err, "expected error from sub1 mid-write failure")
	require.False(t, errors.Is(err, reconnect.ErrNotConnected),
		"mid-write error should not be classified as ErrNotConnected")

	// mock2 にペイロードが流れていないことを確認
	select {
	case got := <-mock2.writeCh:
		t.Fatalf("payload unexpectedly resent to sub2: %v", got)
	case <-time.After(100 * time.Millisecond):
		// expected: sub2 には書き込まれない
	}
}

// newAlwaysFailingConnectingTransport は初回接続から常に失敗し、
// StatusConnecting のまま固定される reconnect.Transport を作る。
func newAlwaysFailingConnectingTransport(t *testing.T, subConnID string) *reconnect.Transport {
	t.Helper()
	rt, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			return nil, errors.New("test: initial dial always fails")
		}),
		DialConfig: transport.DialConfig{
			SubConnectionID:   transport.SubConnectionID(subConnID),
			SuperConnectionID: transport.SuperConnectionID(testSuperConnectionID),
			TransportType:     transport.NegotiationNameWebSocket,
		},
		MaxReconnectAttempts: -1, // 設計 1: sub には常に無期限リトライをさせる
		ReconnectInterval:    10 * time.Millisecond,
		HeartbeatInterval:    time.Hour,
		HeartbeatTimeout:     time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)
	return rt
}

// closeMultiAndWait は multi.Transport を閉じて goroutine の終了を待つ。
//
// **t.Cleanup ではなく defer で使うこと。** t.Cleanup はテスト関数の defer が
// 全て走った後に実行されるため、Cleanup で閉じても defer で登録した
// goleak.VerifyNone の検査には間に合わない（statusMonitorLoop / readLoop /
// initialConnect などが生存したまま検査され、必ず leak として落ちる）。
// defer の LIFO により、goleak.VerifyNone より後に登録した本 helper が先に走る。
//
// 閾値超過による giveUp が既に Close している場合があるため、エラーは無視する
// （既存の closeAndWait は require.NoError するのでこの用途には使えない）。
func closeMultiAndWait(mt *Transport) {
	_ = mt.Close()
	time.Sleep(200 * time.Millisecond)
}

// TestMultiTransport_全sub未接続が閾値を超えたらWriteが解放される は spec の
// 受入基準 2 と 7 を検証する。
//
//   - 閾値到達前: 全 sub が Connecting なので waitForWritable がブロックし、
//     multi.Write は即エラーを返さない（Open 直後の正常な過渡状態）
//   - 閾値到達後: 親が全 sub を Close するので、ブロックしていた Write が
//     エラーで返り、OverallStatus が Disconnected になる
func TestMultiTransport_全sub未接続が閾値を超えたらWriteが解放される(t *testing.T) {
	defer goleak.VerifyNone(t)

	rt1 := newAlwaysFailingConnectingTransport(t, "sub1")
	rt2 := newAlwaysFailingConnectingTransport(t, "sub2")

	id1 := transport.SubConnectionID("transport1")
	id2 := transport.SubConnectionID("transport2")

	// mockTransportSelector は SelectAvailableTransport 経由で status-aware に
	// フォールバックするが、ここでは全 sub が非 Connected なので結果は変わらない。
	// waitForWritable の Connecting 分岐へ確実に到達させるため固定セレクタを使う。
	selector := newFixedTransportSelector(id1)

	const timeout = 300 * time.Millisecond
	mt, err := NewTransport(TransportConfig{
		TransportMap:                TransportMap{id1: rt1, id2: rt2},
		TransportSelector:           selector,
		Logger:                      log.NewNop(),
		NoConnectedTransportTimeout: timeout,
	})
	require.NoError(t, err)
	defer closeMultiAndWait(mt)

	done := make(chan error, 1)
	go func() { done <- mt.Write([]byte("payload")) }()

	// 受入基準 2: 閾値到達前は即エラーにならない。
	select {
	case err := <-done:
		t.Fatalf("Write が閾値到達前にエラーを返した: %v", err)
	case <-time.After(timeout / 2):
		// 期待どおりブロック継続。
	}

	// 受入基準 7: 閾値超過後は Write がエラーで返る。
	select {
	case err := <-done:
		require.Error(t, err, "閾値超過後の Write はエラーで返るはず")
	case <-time.After(5 * time.Second):
		t.Fatal("閾値を超えても Write が解放されなかった")
	}

	require.Equal(t, MultiOverallStatusDisconnected, mt.OverallStatus())
}

// TestMultiTransport_全subReconnectingが閾値を超えたら畳まれる は spec の受入基準 3 を検証する。
// 閾値到達前の Write は即エラー（waitForWritable の Reconnecting 分岐）であり、
// 受入基準 2（Connecting 版）と非対称になるのが仕様。
func TestMultiTransport_全subReconnectingが閾値を超えたら畳まれる(t *testing.T) {
	defer goleak.VerifyNone(t)

	mock1 := newMockTransport("mock1")
	rt1 := newFailingReconnectTransport(t, mock1, "sub1")
	mock2 := newMockTransport("mock2")
	rt2 := newFailingReconnectTransport(t, mock2, "sub2")

	id1 := transport.SubConnectionID("transport1")
	id2 := transport.SubConnectionID("transport2")

	waitForConnected(t, rt1)
	waitForConnected(t, rt2)

	mt, err := NewTransport(TransportConfig{
		TransportMap:                TransportMap{id1: rt1, id2: rt2},
		TransportSelector:           newMockTransportSelector(id1),
		Logger:                      log.NewNop(),
		NoConnectedTransportTimeout: 300 * time.Millisecond,
	})
	require.NoError(t, err)
	defer closeMultiAndWait(mt)

	// 両方の下層を落とすと newFailingReconnectTransport のダイアラーは以後常に失敗し、
	// MaxReconnectAttempts=-1 なので Reconnecting のまま固定される。
	mock1.Close()
	mock2.Close()

	require.Eventually(t,
		func() bool {
			return rt1.Status() == reconnect.StatusReconnecting &&
				rt2.Status() == reconnect.StatusReconnecting
		},
		5*time.Second, 10*time.Millisecond,
		"両 sub が Reconnecting になるはず")

	// 受入基準 3 の非対称性: Reconnecting では waitForWritable が即エラーを返すため、
	// 閾値到達前でも multi.Write はブロックせずエラーになる（Connecting 版とは異なる）。
	require.Error(t, mt.Write([]byte("payload")),
		"Reconnecting 中の Write は閾値到達前でも即エラーになる")

	require.Eventually(t,
		func() bool { return mt.OverallStatus() == MultiOverallStatusDisconnected },
		5*time.Second, 20*time.Millisecond,
		"全 sub が Reconnecting のまま閾値を超えたら Disconnected になるはず")

	// 閾値超過後は Write もエラーになる。
	require.Error(t, mt.Write([]byte("payload")))
}

// TestMultiTransport_閾値到達前に復帰したら畳まれない は spec の受入基準 4 を検証する。
func TestMultiTransport_閾値到達前に復帰したら畳まれない(t *testing.T) {
	defer goleak.VerifyNone(t)

	mock1 := newMockTransport("mock1")
	rt1 := newTestReconnectTransport(t, mock1, "sub1")
	rt2 := newAlwaysFailingConnectingTransport(t, "sub2")

	id1 := transport.SubConnectionID("transport1")
	id2 := transport.SubConnectionID("transport2")

	waitForConnected(t, rt1)

	mt, err := NewTransport(TransportConfig{
		TransportMap:                TransportMap{id1: rt1, id2: rt2},
		TransportSelector:           newMockTransportSelector(id1),
		Logger:                      log.NewNop(),
		NoConnectedTransportTimeout: 200 * time.Millisecond,
	})
	require.NoError(t, err)
	defer closeMultiAndWait(mt)

	// sub1 が Connected なので、閾値の何倍待っても畳まれない。
	require.Never(t,
		func() bool { return mt.OverallStatus() == MultiOverallStatusDisconnected },
		1*time.Second, 20*time.Millisecond,
		"1 本でも Connected なら計測はリセットされ続ける")
}

// TestMultiTransport_閾値0なら畳まれない は spec の受入基準 5 を検証する。
// MaxReconnectAttempts=-1（無期限）を CalcNoConnectedTransportTimeout に通すと 0 になり、
// この経路に落ちる。
func TestMultiTransport_閾値0なら畳まれない(t *testing.T) {
	defer goleak.VerifyNone(t)

	rt1 := newAlwaysFailingConnectingTransport(t, "sub1")
	rt2 := newAlwaysFailingConnectingTransport(t, "sub2")

	id1 := transport.SubConnectionID("transport1")
	id2 := transport.SubConnectionID("transport2")

	mt, err := NewTransport(TransportConfig{
		TransportMap:      TransportMap{id1: rt1, id2: rt2},
		TransportSelector: newMockTransportSelector(id1),
		Logger:            log.NewNop(),
		// NoConnectedTransportTimeout は未設定（= 0）。
		StatusCheckInterval: 20 * time.Millisecond,
	})
	require.NoError(t, err)
	defer closeMultiAndWait(mt)

	require.Never(t,
		func() bool { return mt.OverallStatus() == MultiOverallStatusDisconnected },
		1*time.Second, 20*time.Millisecond,
		"閾値 0（無効）では何時間経っても畳まない（現行互換）")
}
