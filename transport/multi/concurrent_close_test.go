package multi_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aptpod/iscp-go/v2/log"
	"github.com/aptpod/iscp-go/v2/transport"
	. "github.com/aptpod/iscp-go/v2/transport/multi"
	"github.com/aptpod/iscp-go/v2/transport/reconnect"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// ---------- Helpers ----------

// countingMockTransport は mockTransport（transport_test.go）相当だが、Close の呼び出し
// 回数を記録できる。multi.Transport.CloseWithStatus（transport.go:402-417）にガードが
// 無いこと（P2）の観測用に concurrent_close_test.go 専用で用意する。
// newTestReconnectTransport は引数型が *mockTransport 固定のため使えず、
// newReconnectTransportWithMock を別途用意して使う。
type countingMockTransport struct {
	mu         sync.Mutex
	isClosed   bool
	closeCount int
	name       transport.Name

	readCh  chan []byte
	closeCh chan struct{}
}

func newCountingMockTransport(name string) *countingMockTransport {
	return &countingMockTransport{
		name:    transport.Name(name),
		readCh:  make(chan []byte, 100),
		closeCh: make(chan struct{}),
	}
}

func (m *countingMockTransport) Read() ([]byte, error) {
	select {
	case data := <-m.readCh:
		return data, nil
	case <-m.closeCh:
		return nil, errors.New("mock: transport closed")
	}
}

func (m *countingMockTransport) Write(_ []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.isClosed {
		return errors.New("mock: transport closed")
	}
	return nil
}

func (m *countingMockTransport) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeCount++
	if !m.isClosed {
		m.isClosed = true
		close(m.closeCh)
	}
	return nil
}

func (m *countingMockTransport) CloseWithStatus(_ transport.CloseStatus) error {
	return m.Close()
}

func (m *countingMockTransport) CloseCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closeCount
}

func (m *countingMockTransport) Name() transport.Name { return m.name }

func (m *countingMockTransport) NegotiationParams() transport.NegotiationParams {
	return transport.NegotiationParams{}
}

func (m *countingMockTransport) AsUnreliable() (transport.UnreliableTransport, bool) {
	return nil, false
}

func (m *countingMockTransport) RxBytesCounterValue() uint64 { return 0 }
func (m *countingMockTransport) TxBytesCounterValue() uint64 { return 0 }

// newReconnectTransportWithMock は、指定した transport.Transport を返す固定 dialer で
// reconnect.Transport を作る。newTestReconnectTransport は引数型が *mockTransport 固定の
// ため、countingMockTransport など別のモック型を渡せない場合に使う。
func newReconnectTransportWithMock(t *testing.T, mock transport.Transport, subConnID string) *reconnect.Transport {
	t.Helper()
	rt, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			return mock, nil
		}),
		DialConfig: transport.DialConfig{
			SubConnectionID:   transport.SubConnectionID(subConnID),
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
	return rt
}

// newReopenableReconnectTransport は dial のたびに新しい mockTransport を生成する dialer で
// reconnect.Transport を作る。無期限リトライ（-1）なので、下層 mock を Close すると
// Reconnecting になり、新しい mock で再度 Connected に戻るという往復を繰り返せる。
// 返り値の drop 関数は現在の下層 mock を Close する。
func newReopenableReconnectTransport(t *testing.T, subConnID string) (rt *reconnect.Transport, drop func()) {
	t.Helper()
	var mu sync.Mutex
	var current *mockTransport
	rt, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			mu.Lock()
			current = newMockTransport(subConnID)
			m := current
			mu.Unlock()
			return m, nil
		}),
		DialConfig: transport.DialConfig{
			SubConnectionID:   transport.SubConnectionID(subConnID),
			SuperConnectionID: transport.SuperConnectionID(testSuperConnectionID),
			TransportType:     transport.NegotiationNameWebSocket,
		},
		MaxReconnectAttempts: -1,
		ReconnectInterval:    2 * time.Millisecond,
		HeartbeatInterval:    time.Hour,
		HeartbeatTimeout:     time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)
	drop = func() {
		mu.Lock()
		m := current
		mu.Unlock()
		if m != nil {
			m.Close()
		}
	}
	return rt, drop
}

// blockingWriteMockTransport は Write / Read が unblock() されるまでブロックし続ける
// モック。P4（下層 Write のブロック中に他 sub へフォールバックしないこと）の検証専用。
type blockingWriteMockTransport struct {
	name      transport.Name
	unblockCh chan struct{}
	once      sync.Once
}

func newBlockingWriteMockTransport(name string) *blockingWriteMockTransport {
	return &blockingWriteMockTransport{
		name:      transport.Name(name),
		unblockCh: make(chan struct{}),
	}
}

func (m *blockingWriteMockTransport) Read() ([]byte, error) {
	<-m.unblockCh
	return nil, errors.New("mock: unblocked")
}

func (m *blockingWriteMockTransport) Write(_ []byte) error {
	<-m.unblockCh
	return errors.New("mock: unblocked")
}

func (m *blockingWriteMockTransport) Close() error {
	m.unblock()
	return nil
}

func (m *blockingWriteMockTransport) CloseWithStatus(_ transport.CloseStatus) error {
	return m.Close()
}

func (m *blockingWriteMockTransport) unblock() {
	m.once.Do(func() { close(m.unblockCh) })
}

func (m *blockingWriteMockTransport) Name() transport.Name { return m.name }

func (m *blockingWriteMockTransport) NegotiationParams() transport.NegotiationParams {
	return transport.NegotiationParams{}
}

func (m *blockingWriteMockTransport) AsUnreliable() (transport.UnreliableTransport, bool) {
	return nil, false
}

func (m *blockingWriteMockTransport) RxBytesCounterValue() uint64 { return 0 }
func (m *blockingWriteMockTransport) TxBytesCounterValue() uint64 { return 0 }

// ---------- Tests ----------

// TestMultiTransport_並行WriteとClose は stressGoroutines 本が Write 中に別 goroutine が
// Close しても、全 Write が有限時間で返り（ハングしない）、-race がクリーンであることを検証する。
//
// 既存の mockTransport（transport_test.go）ではなく countingMockTransport を使う。
// mockTransport.Write は内部の writeCh（バッファ 100）に書き込むだけで誰も読み出さないため、
// stressGoroutines × stressIterations 回（stress ビルドで 32 × 200 = 6400）の Write を
// 行うと確実にバッファが枯渇して Write がブロックしたままになる。実際に stress ビルドで
// 15 分タイムアウトするデッドロックを確認した（mockTransport.Write が writeCh への
// send でブロックし、それを保持する reconnect.Transport の内部 mutex を他の Write /
// Close 呼び出しが待ち続ける）。countingMockTransport.Write は何もバッファせず即座に
// 成功するため、この問題が起きない。
func TestMultiTransport_並行WriteとClose(t *testing.T) {
	defer goleak.VerifyNone(t)

	mock1 := newCountingMockTransport("mock1")
	rt1 := newReconnectTransportWithMock(t, mock1, "sub1")
	waitForConnected(t, rt1)

	id1 := transport.SubConnectionID("transport1")
	selector := newFixedTransportSelector(id1)

	mt, err := NewTransport(TransportConfig{
		TransportMap:      TransportMap{id1: rt1},
		TransportSelector: selector,
		Logger:            log.NewNop(),
	})
	require.NoError(t, err)
	// goleak との呼び出し順序に注意: defer は LIFO なので、この defer は
	// 上の defer goleak.VerifyNone(t) より先に（後で登録したものが先に）実行される。
	defer closeMultiAndWait(mt)

	var wg sync.WaitGroup
	for g := 0; g < stressGoroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < stressIterations; i++ {
				_ = mt.Write([]byte("data")) // Close 前後どちらもありうる。エラーの有無は問わない。
			}
		}()
	}

	// Write が走っている最中を狙って Close する。
	go func() {
		time.Sleep(time.Millisecond)
		_ = mt.Close()
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("concurrent Write did not finish within 10s (possible hang)")
	}
}

// TestMultiTransport_Closeの多重呼び出し は multi.Transport.Close を 2 回呼んでも
// パニックせず返ることを検証する。あわせて各 sub の下層 Close 呼び出し回数を記録する。
//
// P2（あるべき姿とのずれ）: multi.Transport.CloseWithStatus（transport.go:402-417）には
// closeOnce 相当のガードが無いため、呼ぶたびに全 sub の CloseWithStatus を再度呼ぶ。
// さらに reconnect.Transport 側にもガードが無い（Task B / P2）ため、multi.Close() を
// N 回呼べば下層 mock.Close() も N 回呼ばれる。本タスクでは production コードを
// 変更しないため、現状の回数をそのまま記録する。
//
// 2026-07-31 Task E の修正時、全 sub が Disconnected になったら closeAll を
// 1 回だけ非同期実行する経路（giveUpOnce 経由、transport.go:330-333）が追加され、
// 1 回目の mt.Close() で各 sub が Disconnected に遷移した際にこの経路が誤って
// 発火し、期待値が一時的に 2 から 3 になっていた。これは意図しない Close の
// 再入だったため、CloseWithStatus の先頭で giveUpOnce を消費する形に修正済み
// （transport.go:405-410）。この期待値 2 は「明示 Close では teardown 経路が
// 発火しないこと」の回帰検出点になる。
func TestMultiTransport_Closeの多重呼び出し(t *testing.T) {
	defer goleak.VerifyNone(t)

	mock1 := newCountingMockTransport("mock1")
	mock2 := newCountingMockTransport("mock2")
	rt1 := newReconnectTransportWithMock(t, mock1, "sub1")
	rt2 := newReconnectTransportWithMock(t, mock2, "sub2")
	waitForConnected(t, rt1)
	waitForConnected(t, rt2)

	id1 := transport.SubConnectionID("transport1")
	id2 := transport.SubConnectionID("transport2")

	mt, err := NewTransport(TransportConfig{
		TransportMap:      TransportMap{id1: rt1, id2: rt2},
		TransportSelector: newFixedTransportSelector(id1),
		Logger:            log.NewNop(),
	})
	require.NoError(t, err)

	require.NotPanics(t, func() {
		require.NoError(t, mt.Close())
	})
	require.NotPanics(t, func() {
		_ = mt.Close() // 2 回目もパニックしないことのみ要求。エラーの有無は問わない。
	})

	time.Sleep(200 * time.Millisecond) // goroutine の後始末を待つ

	c1, c2 := mock1.CloseCount(), mock2.CloseCount()
	t.Logf("P2: after multi.Close() x2, underlying Close was called mock1=%d mock2=%d times (no close-once guard in multi nor reconnect; giveUpOnce is consumed by CloseWithStatus so closeAll does not re-enter)", c1, c2)
	require.Equal(t, 2, c1, "P2: sub1 の下層 Close 呼び出し回数（明示 Close x2。giveUpOnce 消費により closeAll は再入しない）")
	require.Equal(t, 2, c2, "P2: sub2 の下層 Close 呼び出し回数（明示 Close x2。giveUpOnce 消費により closeAll は再入しない）")
}

// TestMultiTransport_ブロック中のWriteがCloseで解放される は spec 受入基準 10 の直接検証。
// 全 sub を Connecting に固定し（newAlwaysFailingConnectingTransport）、
// NoConnectedTransportTimeout=0（畳まない設定）で Write をブロックさせた状態から、
// 別 goroutine の Close で Write が有限時間でエラー返却されることを stressIterationsSlow 回
// 繰り返して確認する。
//
// 1 周回に固定の time.Sleep（NewTransport 生成コスト込みで約 250ms 実測）を挟むため、
// stressIterations（stress ビルドで 200）をそのまま使うと単純計算で約 50 秒かかる。
// 実時間依存のため stressIterationsSlow を使う（stress_params_*.go 参照）。
func TestMultiTransport_ブロック中のWriteがCloseで解放される(t *testing.T) {
	defer goleak.VerifyNone(t)

	for i := 0; i < stressIterationsSlow; i++ {
		if err := writeBlockedByCloseRound(t, i); err != nil {
			t.Fatalf("iteration %d failed: %v", i, err)
		}
	}
}

func writeBlockedByCloseRound(t *testing.T, iter int) error {
	t.Helper()

	rt1 := newAlwaysFailingConnectingTransport(t, "sub1")
	rt2 := newAlwaysFailingConnectingTransport(t, "sub2")

	id1 := transport.SubConnectionID("transport1")
	id2 := transport.SubConnectionID("transport2")

	mt, err := NewTransport(TransportConfig{
		TransportMap:      TransportMap{id1: rt1, id2: rt2},
		TransportSelector: newFixedTransportSelector(id1),
		Logger:            log.NewNop(),
		// NoConnectedTransportTimeout は未設定（0）＝畳まない設定。
	})
	if err != nil {
		return err
	}
	defer closeMultiAndWait(mt)

	done := make(chan error, 1)
	go func() { done <- mt.Write([]byte("payload")) }()

	// 畳まれない設定なので、Close するまで Write はブロックし続けるはず。
	select {
	case err := <-done:
		return fmt.Errorf("iteration %d: Write returned before Close was called: %v", iter, err)
	case <-time.After(50 * time.Millisecond):
		// 期待どおりブロック継続。
	}

	go func() { _ = mt.Close() }()

	select {
	case err := <-done:
		if err == nil {
			return fmt.Errorf("iteration %d: Write succeeded after Close (expected an error)", iter)
		}
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("iteration %d: Write did not return within 5s after Close", iter)
	}
}

// TestMultiTransport_giveUpと明示Closeの競合 は P3（giveUp と明示 Close の競合）を検証する。
// 閾値を極端に短く設定して giveUp（updateOverallStatus 経由の giveUpOnce.Do(...)）を誘発
// しつつ、ほぼ同時に明示 Close を呼ぶ。両方が有限時間で返り、-race がクリーンで goleak が
// 通ることを、タイミングをずらしながら stressIterationsSlow 回繰り返して確認する。
// rand は使わず time.Duration(i%5) * time.Millisecond で再現可能にする。
//
// 1 周回に固定の待ち（wait + 後始末 50ms）を挟むため、stressIterations（stress ビルドで
// 200）をそのまま使うと実行時間が膨れる。実時間依存のため stressIterationsSlow を使う
// （stress_params_*.go 参照）。
func TestMultiTransport_giveUpと明示Closeの競合(t *testing.T) {
	defer goleak.VerifyNone(t)

	for i := 0; i < stressIterationsSlow; i++ {
		if err := giveUpCloseRaceRound(t, i); err != nil {
			t.Fatalf("iteration %d failed: %v", i, err)
		}
	}
}

func giveUpCloseRaceRound(t *testing.T, iter int) error {
	t.Helper()

	rt1 := newAlwaysFailingConnectingTransport(t, "sub1")

	id1 := transport.SubConnectionID("transport1")

	mt, err := NewTransport(TransportConfig{
		TransportMap:                TransportMap{id1: rt1},
		TransportSelector:           newFixedTransportSelector(id1),
		Logger:                      log.NewNop(),
		NoConnectedTransportTimeout: 5 * time.Millisecond, // 極端に短くして giveUp を誘発
		StatusCheckInterval:         2 * time.Millisecond,
	})
	if err != nil {
		return err
	}

	// giveUp の発火タイミングをまたぐように、周回ごとにわずかに異なる待ち時間を入れてから
	// 明示 Close を呼ぶ（rand は使わず再現可能にする）。
	wait := time.Duration(iter%5) * time.Millisecond
	time.Sleep(wait)

	closeDone := make(chan error, 1)
	go func() { closeDone <- mt.Close() }()

	select {
	case <-closeDone:
	case <-time.After(5 * time.Second):
		return fmt.Errorf("iteration %d: Close did not return within 5s (wait=%v)", iter, wait)
	}

	// giveUp 経路（別 goroutine で起動される）も含めて後始末が終わるのを待つ。
	time.Sleep(50 * time.Millisecond)
	return nil
}

// TestMultiTransport_閾値直前の復帰を繰り返す は、閾値の手前で 1 本を Connected に戻す→
// また落とす、を stressIterationsSlow 回繰り返し、畳まれないこと（noConnectedTracker の計測が
// 都度リセットされること）を検証する。fakeClock ではなく実時間で回し、level-trigger の
// 取りこぼしを狙う。
//
// 1 周回に固定の time.Sleep（閾値の70% + waitForConnected 待ち）を挟むため、
// stressIterations（stress ビルドで 200）をそのまま使うと実行時間が単純に膨れる。
// 実時間依存のため stressIterationsSlow を使う（stress_params_*.go 参照）。
func TestMultiTransport_閾値直前の復帰を繰り返す(t *testing.T) {
	defer goleak.VerifyNone(t)

	const threshold = 100 * time.Millisecond
	rt1, drop1 := newReopenableReconnectTransport(t, "sub1")
	waitForConnected(t, rt1)

	id1 := transport.SubConnectionID("transport1")
	mt, err := NewTransport(TransportConfig{
		TransportMap:                TransportMap{id1: rt1},
		TransportSelector:           newFixedTransportSelector(id1),
		Logger:                      log.NewNop(),
		NoConnectedTransportTimeout: threshold,
		StatusCheckInterval:         10 * time.Millisecond,
	})
	require.NoError(t, err)
	defer closeMultiAndWait(mt)

	for i := 0; i < stressIterationsSlow; i++ {
		drop1() // Connected → Reconnecting（noConnectedTracker の計測が開始する）

		// 閾値の 70% だけ待ってから復帰させる（level-trigger の取りこぼしを狙う）。
		time.Sleep(threshold * 7 / 10)
		if mt.OverallStatus() == MultiOverallStatusDisconnected {
			t.Fatalf("iteration %d: overall status became Disconnected before the threshold", i)
		}

		waitForConnected(t, rt1) // 新しい mock で Connected に戻るまで待つ
		if mt.OverallStatus() == MultiOverallStatusDisconnected {
			t.Fatalf("iteration %d: overall status is Disconnected right after recovery", i)
		}
	}
}

// TestMultiTransport_ブロック中のWriteは他subへフォールバックしない は、選択された
// sub-conn の下層 Write がブロックしている間、multi.Transport.Write が健全な別 sub-conn
// へフォールバックせずブロックし続けるという「現状の挙動」を記録するテストです（P4）。
// あるべき姿かどうかの判断は保留し、まず事実を固定します。
//
// writeOnce（transport.go:539-590）は選択した sub の下層 Write が返るまで次の判断に
// 進めないため、下層 Write 自体がブロックする実装では他 sub-conn へのフォールバックが
// 機能しません。
func TestMultiTransport_ブロック中のWriteは他subへフォールバックしない(t *testing.T) {
	defer goleak.VerifyNone(t)

	blockingMock := newBlockingWriteMockTransport("sub1")
	rt1 := newReconnectTransportWithMock(t, blockingMock, "sub1")
	waitForConnected(t, rt1)

	mock2 := newMockTransport("mock2")
	rt2 := newTestReconnectTransport(t, mock2, "sub2")
	waitForConnected(t, rt2)

	id1 := transport.SubConnectionID("transport1")
	id2 := transport.SubConnectionID("transport2")
	selector := newFixedTransportSelector(id1) // 常に sub1（ブロックする方）を選ばせる

	mt, err := NewTransport(TransportConfig{
		TransportMap:      TransportMap{id1: rt1, id2: rt2},
		TransportSelector: selector,
		Logger:            log.NewNop(),
	})
	require.NoError(t, err)
	defer closeMultiAndWait(mt)

	done := make(chan error, 1)
	go func() { done <- mt.Write([]byte("payload")) }()

	select {
	case err := <-done:
		t.Fatalf("Write returned (err=%v) despite sub1's underlying Write being blocked — フォールバックが発生した可能性がある", err)
	case <-time.After(300 * time.Millisecond):
		t.Log("P4 confirmed: Write blocked on sub1 without falling back to sub2 (current behavior)")
	}

	// mock2 には書き込まれていないはず。
	select {
	case got := <-mock2.writeCh:
		t.Fatalf("payload unexpectedly reached sub2 despite selector choosing sub1: %v", got)
	default:
	}

	// 後始末: ブロックしている Write を解放する。
	blockingMock.unblock()
	select {
	case err := <-done:
		t.Logf("Write returned after unblocking: err=%v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Write did not return even after unblocking the underlying Write")
	}
}
