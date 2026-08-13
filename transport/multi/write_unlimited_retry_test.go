package multi_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
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

// capturingLogger は Warnf の出力だけを記録するテスト用ロガー。
// それ以外のメソッドは埋め込んだ Nop ロガーに委譲する。
type capturingLogger struct {
	log.Logger
	mu    sync.Mutex
	warns []string
}

func newCapturingLogger() *capturingLogger {
	return &capturingLogger{Logger: log.NewNop()}
}

func (l *capturingLogger) Warnf(_ context.Context, format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warns = append(l.warns, fmt.Sprintf(format, args...))
}

func (l *capturingLogger) joinedWarns() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.warns, "\n")
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
//
// sub1 の Dialer は初回接続のみ mock1 を返し、以後は必ずエラーを返す
// （TestReconnectTransport_Write_FailsAfterFiniteRetriesExhausted と同じパターン）。
// これにより、mock1.Close() 後に readLoop が切断を検知して再接続を試みても
// 同じ壊れた mock1 で "再接続成功" と誤判定されて StatusConnected に戻ることがなく、
// mock1.Close() 直後（StatusConnected のうちに）Write を発行するタイミングウィンドウが
// 安定する（初回コードは Dialer が常に同じ mock1 を返すため、再接続の
// 成功/失敗サイクルが繰り返し発生し、負荷次第で Write 時に sub1 が
// Connected 以外と判定されて sub2 にフォールバックしてしまうことがあった）。
func TestMultiTransport_Write_DoesNotFallbackOnPartialSendError(t *testing.T) {
	mock1 := newMockTransport("mock1")
	var dialCount1 atomic.Int32
	rt1, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			if dialCount1.Add(1) == 1 {
				return mock1, nil
			}
			return nil, errors.New("test: reconnect always fails after initial connect")
		}),
		DialConfig: transport.DialConfig{
			SubConnectionID:   "sub1",
			SuperConnectionID: transport.SuperConnectionID(testSuperConnectionID),
			TransportType:     transport.NegotiationNameWebSocket,
		},
		MaxReconnectAttempts: 1,
		ReconnectInterval:    10 * time.Millisecond,
		HeartbeatInterval:    time.Hour,
		HeartbeatTimeout:     time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt1.Close() })

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

// TestMultiTransport_Write_FallbacksOnAlreadyClosedError は
// TestMultiTransport_Write_DoesNotFallbackOnPartialSendError の対になる
// positive ケース（F3）。sub1 の下層 Write が transport.ErrAlreadyClosed
// （writeRaw の TOCTOU 対策が ErrNotConnected へ変換する対象そのもの）で
// 失敗した場合、multi.Transport が sub2 へフォールバックしてペイロードが
// 届くことを検証する。
//
// reconnect.Transport.writeRaw は下層 tr.Write のエラーが
// errors.Is(err, errors.ErrConnectionClosed)（== transport.ErrAlreadyClosed）
// のときに reconnect.ErrNotConnected でラップする（TOCTOU 修正）。この変換を
// 削除しても、既存の websocket 側のテストは reconnect の変換層を経由しない
// ため検出できない（false green）。本テストは reconnect.Transport 経由で
// multi.Transport のフォールバックまで検証することで、この変換の有無が
// 実際に挙動へ影響することを保証する。
func TestMultiTransport_Write_FallbacksOnAlreadyClosedError(t *testing.T) {
	mock1 := newMockTransport("mock1")
	mock1.writeErrWhenClosed = transport.ErrAlreadyClosed
	var dialCount1 atomic.Int32
	rt1, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			if dialCount1.Add(1) == 1 {
				return mock1, nil
			}
			return nil, errors.New("test: reconnect always fails after initial connect")
		}),
		DialConfig: transport.DialConfig{
			SubConnectionID:   "sub1",
			SuperConnectionID: transport.SuperConnectionID(testSuperConnectionID),
			TransportType:     transport.NegotiationNameWebSocket,
		},
		MaxReconnectAttempts: 1,
		ReconnectInterval:    10 * time.Millisecond,
		HeartbeatInterval:    time.Hour,
		HeartbeatTimeout:     time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt1.Close() })

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
	// transport.ErrAlreadyClosed を返す。writeRaw の TOCTOU 対策により
	// これは ErrNotConnected として fallback 対象になる。
	mock1.Close()

	err = mt.Write([]byte("payload"))
	require.NoError(t, err, "should fall back to sub2 when sub1's underlying Write fails with ErrAlreadyClosed")

	select {
	case got := <-mock2.writeCh:
		// useV4Protocol の場合 payload 先頭に 1 バイトの type タグが付くため、
		// 完全一致ではなく suffix で照合する（TestMultiTransport_Write_FallbackDuringUnlimitedRetry
		// と同様に内容そのものより「届いたこと」を主眼にする）。
		require.True(t, bytes.HasSuffix(got, []byte("payload")), "unexpected payload: %v", got)
	case <-time.After(time.Second):
		t.Fatal("payload did not reach sub2 within 1s")
	}
}

// TestMultiTransport_Write_LogsWarningOnFallback は、選択された sub-conn への
// 書き込みが writeTimeout（context.DeadlineExceeded）で失敗し、フォールバックが
// 成功した場合に警告ログが残ることを検証する。
//
// writeOnce はフォールバックが成功すると上位に error を返さないため、ログが
// なければ「1 本の sub-conn が writeTimeout まで stall した」ことがアプリからも
// 運用からも観測できない。
func TestMultiTransport_Write_LogsWarningOnFallback(t *testing.T) {
	mock1 := newMockTransport("mock1")
	rt1 := newTestReconnectTransport(t, mock1, "sub1")
	mock2 := newMockTransport("mock2")
	rt2 := newTestReconnectTransport(t, mock2, "sub2")

	waitForConnected(t, rt1)
	waitForConnected(t, rt2)

	// Read はブロックしたまま Write だけを失敗させるため rt1 の Status は
	// Connected のまま保たれ、selector は rt1 を選び続ける。これにより
	// writeOnce 内のフォールバックパスをタイミング非依存で通せる。
	mock1.SetAlwaysFailWrite(context.DeadlineExceeded)

	id1 := transport.SubConnectionID("transport1")
	id2 := transport.SubConnectionID("transport2")
	selector := newMockTransportSelector(id1)

	logger := newCapturingLogger()
	mt, err := NewTransport(TransportConfig{
		TransportMap: TransportMap{
			id1: rt1,
			id2: rt2,
		},
		TransportSelector:   selector,
		Logger:              logger,
		StatusCheckInterval: 100 * time.Millisecond,
	})
	require.NoError(t, err)
	t.Cleanup(func() { closeMultiAndWait(mt) })

	require.NoError(t, mt.Write([]byte("payload")))

	select {
	case got := <-mock2.writeCh:
		require.True(t, bytes.HasSuffix(got, []byte("payload")), "unexpected payload: %v", got)
	case <-time.After(time.Second):
		t.Fatal("payload did not reach sub2 within 1s")
	}

	warns := logger.joinedWarns()
	require.Contains(t, warns, "Write fell back from transport",
		"フォールバック成功は上位にエラーを返さないため、ログが唯一の観測点になる")
	require.Contains(t, warns, context.DeadlineExceeded.Error(),
		"writeTimeout 由来であることがログから判別できる必要がある")
}

// TestMultiTransport_Write_RetriesWhenAllSubsFailWithAlreadyClosedError は
// F3 で見つかった追加ケース。writeRaw の TOCTOU 対策（下層 Write が
// errors.ErrConnectionClosed のとき reconnect.ErrNotConnected へ変換する）は、
// 1 本の sub だけが失敗し他の sub へ fallback できる場合は
// isFallbackableWriteError が transport.ErrAlreadyClosed を直接判定するため
// 効果が表に出ない（TestMultiTransport_Write_FallbacksOnAlreadyClosedError
// 参照）。効果が可観測になるのは「全 sub が下層 Write で失敗する」場合で、
// multi.Transport.Write は errAllNotConnected（= 全 sub が
// reconnect.ErrNotConnected で失敗）のときだけ内部リトライを続ける
// （transport.go:518-533, 601-605）。下層エラーが ErrNotConnected へ変換
// されないと allNotConnected が false になり、Write は即座にエラーで返る。
//
// 両方の sub-conn を mock1.Close()/mock2.Close() で落とすと、reconnect.Transport
// の Status が Connected → Reconnecting/Disconnected へ遷移するタイミング
// ウィンドウを両方同時に踏む必要があり不安定になる（実際 dialCount パターンでも
// flaky だった）。そのため mock の Read はブロックしたまま Write だけを常に
// 失敗させ、Status を Connected に保ったまま下層 Write のみが失敗する状況を
// タイミング非依存で作る。
func TestMultiTransport_Write_RetriesWhenAllSubsFailWithAlreadyClosedError(t *testing.T) {
	mock1 := newMockTransport("mock1")
	rt1 := newTestReconnectTransport(t, mock1, "sub1")
	mock2 := newMockTransport("mock2")
	rt2 := newTestReconnectTransport(t, mock2, "sub2")

	waitForConnected(t, rt1)
	waitForConnected(t, rt2)

	mock1.SetAlwaysFailWrite(transport.ErrAlreadyClosed)
	mock2.SetAlwaysFailWrite(transport.ErrAlreadyClosed)

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
	t.Cleanup(func() { closeMultiAndWait(mt) })

	done := make(chan error, 1)
	go func() { done <- mt.Write([]byte("payload")) }()

	// writeRaw が ErrNotConnected へ変換していれば allNotConnected=true と
	// なり、いずれかの sub が復帰するまで Write は内部リトライを続けブロック
	// し続ける。変換されていなければ即座にエラーで返ってしまう。
	select {
	case err := <-done:
		t.Fatalf("Write should keep retrying (stay blocked) when all subs fail with ErrAlreadyClosed, got: %v", err)
	case <-time.After(300 * time.Millisecond):
		// 期待どおりブロック継続。
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

	// 受入基準 3（2026-07-31 改訂）: 全 sub が Reconnecting でも、閾値到達までは
	// multi.Write がブロックし続ける。Connecting 版（受入基準 2）と対称。
	done := make(chan error, 1)
	go func() { done <- mt.Write([]byte("payload")) }()

	select {
	case err := <-done:
		t.Fatalf("Write が閾値到達前にエラーを返した: %v", err)
	case <-time.After(150 * time.Millisecond): // 閾値 300ms の半分
		// 期待どおりブロック継続。
	}

	// 閾値超過後はエラーで返る。
	select {
	case err := <-done:
		require.Error(t, err, "閾値超過後の Write はエラーで返るはず")
	case <-time.After(5 * time.Second):
		t.Fatal("閾値を超えても Write が解放されなかった")
	}

	require.Eventually(t,
		func() bool { return mt.OverallStatus() == MultiOverallStatusDisconnected },
		5*time.Second, 20*time.Millisecond,
		"全 sub が Reconnecting のまま閾値を超えたら Disconnected になるはず")
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
