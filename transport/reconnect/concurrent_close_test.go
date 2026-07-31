package reconnect_test

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aptpod/iscp-go/v2/log"
	"github.com/aptpod/iscp-go/v2/transport"
	. "github.com/aptpod/iscp-go/v2/transport/reconnect"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// ---------- Mock Transport ----------

// mockCountingTransport は transport.Transport と transport.Closer を実装するモック。
// reconnect.Transport の下層トランスポートとして使用する。
// multi_test.mockTransport と異なりパッケージをまたいで再利用できないため、
// concurrent_close_test.go 専用に用意する。Close の呼び出し回数を数えられる点が特徴。
type mockCountingTransport struct {
	mu         sync.Mutex
	isClosed   bool
	closeCount int
	writeCount int
	name       transport.Name

	readCh  chan []byte
	closeCh chan struct{}
}

func newMockCountingTransport(name string) *mockCountingTransport {
	return &mockCountingTransport{
		name:    transport.Name(name),
		readCh:  make(chan []byte, 100),
		closeCh: make(chan struct{}),
	}
}

func (m *mockCountingTransport) Read() ([]byte, error) {
	select {
	case data := <-m.readCh:
		return data, nil
	case <-m.closeCh:
		return nil, errors.New("mock: transport closed")
	}
}

// Write はブロックしない。呼び出し回数のみ記録する。
func (m *mockCountingTransport) Write(_ []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.isClosed {
		return errors.New("mock: transport closed")
	}
	m.writeCount++
	return nil
}

// Close は closeCount を毎回インクリメントする（sync.Once でガードしない）。
// reconnect.Transport.CloseWithStatus が下層 Close を何回呼ぶかを観測するため。
func (m *mockCountingTransport) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeCount++
	if !m.isClosed {
		m.isClosed = true
		close(m.closeCh)
	}
	return nil
}

func (m *mockCountingTransport) CloseWithStatus(_ transport.CloseStatus) error {
	return m.Close()
}

func (m *mockCountingTransport) CloseCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closeCount
}

func (m *mockCountingTransport) Name() transport.Name { return m.name }

func (m *mockCountingTransport) NegotiationParams() transport.NegotiationParams {
	return transport.NegotiationParams{}
}

func (m *mockCountingTransport) AsUnreliable() (transport.UnreliableTransport, bool) {
	return nil, false
}

func (m *mockCountingTransport) RxBytesCounterValue() uint64 { return 0 }
func (m *mockCountingTransport) TxBytesCounterValue() uint64 { return 0 }

// ---------- Helpers ----------

// newReconnectTestTransport は、常に同じ mock を返す dialer で reconnect.Transport を作る。
// v4 プロトコル（TransportType 指定）を有効化する。
func newReconnectTestTransport(t *testing.T, mock transport.Transport, maxReconnectAttempts int) *Transport {
	t.Helper()
	tr, err := Dial(DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			return mock, nil
		}),
		DialConfig: transport.DialConfig{
			SubConnectionID: "sub1",
			TransportType:   transport.NegotiationNameWebSocket, // v4 プロトコルを有効化
		},
		MaxReconnectAttempts: maxReconnectAttempts,
		ReconnectInterval:    10 * time.Millisecond,
		HeartbeatInterval:    time.Hour,
		HeartbeatTimeout:     time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)
	return tr
}

// newFailAfterFirstReconnectTransport は初回接続のみ mock を返し、以後は常に失敗する dialer で
// reconnect.Transport を作る。multi_test.newFailingReconnectTransport 相当。
func newFailAfterFirstReconnectTransport(t *testing.T, mock transport.Transport, maxReconnectAttempts int) *Transport {
	t.Helper()
	var mu sync.Mutex
	connected := false
	tr, err := Dial(DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			mu.Lock()
			defer mu.Unlock()
			if !connected {
				connected = true
				return mock, nil
			}
			return nil, errors.New("test: dial always fails after first connect")
		}),
		DialConfig: transport.DialConfig{
			SubConnectionID: "sub1",
			TransportType:   transport.NegotiationNameWebSocket, // v4 プロトコルを有効化
		},
		MaxReconnectAttempts: maxReconnectAttempts,
		ReconnectInterval:    10 * time.Millisecond,
		HeartbeatInterval:    time.Hour,
		HeartbeatTimeout:     time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)
	return tr
}

func waitForTransportConnected(t *testing.T, tr *Transport) {
	t.Helper()
	require.Eventually(t,
		func() bool { return tr.Status() == StatusConnected },
		5*time.Second, 10*time.Millisecond,
		"reconnect transport should become Connected",
	)
}

// ---------- Tests ----------

// TestReconnectTransport_並行Write は stressGoroutines 本の goroutine が同時に Write しても
// -race で writeMu の排他違反が検出されず、全 Write が有限時間で返る（ハングしない）ことを検証する。
func TestReconnectTransport_並行Write(t *testing.T) {
	defer goleak.VerifyNone(t)

	mock := newMockCountingTransport("mock1")
	tr := newReconnectTestTransport(t, mock, 1)
	// goleak との呼び出し順序に注意: t.Cleanup は defer より後に走るため、
	// defer goleak.VerifyNone(t) より後ろに書いた defer が LIFO で先に実行されるようにする。
	defer func() { _ = tr.Close() }()
	waitForTransportConnected(t, tr)

	var wg sync.WaitGroup
	errCh := make(chan error, stressGoroutines*stressIterations)
	for g := 0; g < stressGoroutines; g++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for i := 0; i < stressIterations; i++ {
				if err := tr.Write(fmt.Appendf(nil, "goroutine-%d-iter-%d", n, i)); err != nil {
					errCh <- err
				}
			}
		}(g)
	}

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
	close(errCh)
	for err := range errCh {
		// 接続を維持したままの並行 Write なので全て成功するはず。
		t.Errorf("unexpected Write error during concurrent write: %v", err)
	}
}

// TestReconnectTransport_Write中にClose は Write ループを回しながら別 goroutine で
// Close した場合に、全ての Write が有限時間で返り（ハングしない）、パニックもしないこと、
// Close 後の Write は必ずエラーになることを検証する。
func TestReconnectTransport_Write中にClose(t *testing.T) {
	defer goleak.VerifyNone(t)

	mock := newMockCountingTransport("mock1")
	tr := newReconnectTestTransport(t, mock, 1)
	waitForTransportConnected(t, tr)

	stop := make(chan struct{})
	writeLoopDone := make(chan struct{})
	require.NotPanics(t, func() {
		go func() {
			defer close(writeLoopDone)
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = tr.Write([]byte("data")) // Close 前は nil、Close 後はエラーになるはず
			}
		}()

		time.Sleep(20 * time.Millisecond) // write ループが回り始めるまで待つ
		require.NoError(t, tr.Close())
		close(stop)
	})

	select {
	case <-writeLoopDone:
	case <-time.After(5 * time.Second):
		t.Fatal("write loop did not stop within 5s after Close (Write may be hanging)")
	}

	// Close 後の Write は必ずエラーで返る。
	err := tr.Write([]byte("after close"))
	require.Error(t, err, "Write after Close should return an error")
}

// TestReconnectTransport_Closeの多重呼び出し は同一インスタンスへの Close 2 回呼び出しが
// パニックせず返ることを検証する。あわせて下層 mock の Close 呼び出し回数を記録する。
//
// P2（あるべき姿とのずれ）: reconnect.Transport.CloseWithStatus（transport.go:634-656）は
// r.transport を nil 化しないため、2 回目の呼び出しでも下層 CloseWithStatus を再度呼ぶ。
// 現状は下層 Close が 2 回呼ばれる。あるべき姿は closeOnce 相当のガードで 1 回に抑えることだが、
// 本タスクでは production コードを変更しないため、現状の回数をそのまま記録する。
func TestReconnectTransport_Closeの多重呼び出し(t *testing.T) {
	defer goleak.VerifyNone(t)

	mock := newMockCountingTransport("mock1")
	tr := newReconnectTestTransport(t, mock, 1)
	waitForTransportConnected(t, tr)

	require.NotPanics(t, func() {
		require.NoError(t, tr.Close())
	})

	require.NotPanics(t, func() {
		_ = tr.Close() // 2 回目もパニックしないことのみ要求。エラーの有無は問わない。
	})

	closeCount := mock.CloseCount()
	t.Logf("P2: underlying mock.Close() was called %d time(s) after Transport.Close() x2 (no close-once guard)", closeCount)
	require.Equal(t, 2, closeCount,
		"P2: CloseWithStatus does not guard against repeated calls; underlying Close is invoked every time")
}

// TestReconnectTransport_並行Close は stressGoroutines 本から同時に Close しても、
// 全て有限時間で返り、-race で競合が検出されないことを検証する。
func TestReconnectTransport_並行Close(t *testing.T) {
	defer goleak.VerifyNone(t)

	mock := newMockCountingTransport("mock1")
	tr := newReconnectTestTransport(t, mock, 1)
	waitForTransportConnected(t, tr)

	var wg sync.WaitGroup
	for g := 0; g < stressGoroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = tr.Close()
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent Close did not finish within 5s")
	}
}

// TestReconnectTransport_Reconnect中にClose は、dial を無期限ブロックさせて
// Reconnecting に固定した状態で Close した場合の挙動を測定する。
//
// P6（あるべき姿とのずれ）: doReconnect（transport.go:792）の r.reconnector.Connect() は
// ctx を honor しないため、Close() は waitForReconnectToFinish（:439-443）で
// blockDial が解放されるまで戻れない。あるべき姿は Close() が有限時間で返ることだが、
// 本タスクでは production コードを変更しないため、現状の挙動を測定して記録する。
func TestReconnectTransport_Reconnect中にClose(t *testing.T) {
	defer goleak.VerifyNone(t)

	blockDial := make(chan struct{})
	var dialCount atomic.Int32
	mock := newMockCountingTransport("mock1")

	tr, err := Dial(DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			if dialCount.Add(1) == 1 {
				return mock, nil
			}
			<-blockDial // 2 回目以降は解放されるまで無期限ブロック（dial がハングする状況を模擬）
			return nil, errors.New("test: dialer unblocked")
		}),
		DialConfig: transport.DialConfig{
			SubConnectionID: "sub1",
			TransportType:   transport.NegotiationNameWebSocket,
		},
		MaxReconnectAttempts: -1,
		ReconnectInterval:    10 * time.Millisecond,
		HeartbeatInterval:    time.Hour,
		HeartbeatTimeout:     time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)
	// blockDial は最後に必ず閉じる（未解放だと reconnect goroutine が残留し goleak が別要因で落ちる）。
	defer func() {
		select {
		case <-blockDial:
		default:
			close(blockDial)
		}
	}()

	waitForTransportConnected(t, tr)

	// 下層を落として再接続をトリガーし、2 回目の dial が blockDial 待ちに入るまで待つ。
	mock.Close()
	require.Eventually(t,
		func() bool { return dialCount.Load() >= 2 },
		5*time.Second, 10*time.Millisecond,
		"dialer should be invoked a second time and block",
	)
	require.Eventually(t,
		func() bool { return tr.Status() == StatusReconnecting },
		5*time.Second, 10*time.Millisecond,
	)

	closeDone := make(chan error, 1)
	go func() { closeDone <- tr.Close() }()

	select {
	case <-closeDone:
		t.Log("P6 NOT reproduced: Close() returned before the blocked dial was released")
	case <-time.After(1 * time.Second):
		t.Log("P6 reproduced: Close() is still blocked 1s after being called, while dial is blocked (dial does not honor ctx)")
	}

	// 後始末: block を解放して Close の完了を確認する。
	close(blockDial)
	select {
	case err := <-closeDone:
		t.Logf("Close() returned after releasing the blocked dial: err=%v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Close() did not return even after releasing the blocked dial")
	}
}

// TestReconnectTransport_Read中にClose は Read でブロック中に Close した場合、
// Read が有限時間でエラーを返すことを検証する。
func TestReconnectTransport_Read中にClose(t *testing.T) {
	defer goleak.VerifyNone(t)

	mock := newMockCountingTransport("mock1")
	tr := newReconnectTestTransport(t, mock, 1)
	waitForTransportConnected(t, tr)

	readDone := make(chan error, 1)
	go func() {
		_, err := tr.Read()
		readDone <- err
	}()

	time.Sleep(20 * time.Millisecond) // Read が readResCh 待ちに入るまで待つ
	require.NoError(t, tr.Close())

	select {
	case err := <-readDone:
		require.Error(t, err, "Read should return an error after Close")
	case <-time.After(5 * time.Second):
		t.Fatal("Read did not return within 5s after Close")
	}
}

// TestReconnectTransport_リトライ枯渇後にgoroutineが残らない は、有限リトライ
// （MaxReconnectAttempts=2）を枯渇させて StatusDisconnected になった後、明示 Close せずに
// goroutine が残らないことを検証する。
//
// P1（あるべき姿とのずれ）: doReconnect（transport.go:775-780）はリトライ枯渇時に
// StatusDisconnected へ遷移させるだけで r.cancel() を呼ばない。そのため v4 プロトコル使用時、
// heartbeatLoop（transport.go:333）が r.ctx.Done() を検知できずに残留する。
// あるべき姿はリトライ枯渇時にも r.cancel() を呼び、関連 goroutine を終了させることだが、
// 本タスクでは production コードを変更しないため、FAIL する見込みのまま記録する。
// 落ちた場合は goleak の出力（残っている goroutine のスタック）を報告に含めること。
func TestReconnectTransport_リトライ枯渇後にgoroutineが残らない(t *testing.T) {
	mock := newMockCountingTransport("mock1")
	tr := newFailAfterFirstReconnectTransport(t, mock, 2) // MaxReconnectAttempts=2 で枯渇させる
	// goleak.VerifyNone の後で必ず後始末する。goleak が FAIL しても t.Cleanup は実行される。
	t.Cleanup(func() { _ = tr.Close() })
	waitForTransportConnected(t, tr)

	mock.Close()
	require.Eventually(t,
		func() bool { return tr.Status() == StatusDisconnected },
		5*time.Second, 10*time.Millisecond,
		"status should become Disconnected after retries are exhausted",
	)

	// 明示 Close せずに goroutine の残留を検査する（P1 により FAIL する見込み）。
	goleak.VerifyNone(t)
}
