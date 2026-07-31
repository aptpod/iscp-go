package multi_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/aptpod/iscp-go/v2/log"
	"github.com/aptpod/iscp-go/v2/transport"
	. "github.com/aptpod/iscp-go/v2/transport/multi"
	"github.com/aptpod/iscp-go/v2/transport/reconnect"
)

// ---------- Mock Transport ----------

// mockTransport は transport.Transport と transport.Closer を実装するモック。
// reconnect.Transport の下層トランスポートとして使用する。
type mockTransport struct {
	readCh   chan []byte
	writeCh  chan []byte
	closeCh  chan struct{}
	mu       sync.Mutex
	isClosed bool
	name     transport.Name

	// writeErrWhenClosed が設定されていれば、isClosed 時に Write はこのエラーを
	// 返す（未設定なら既定の "transport closed" という一般エラーを返す）。
	writeErrWhenClosed error

	// alwaysFailWriteErr が設定されていれば、isClosed に関係なく Write は常に
	// このエラーを返す。Read はブロックしたままなので readLoop は切断を検知
	// せず、reconnect.Transport の Status は Connected のまま変わらない
	// （下層 Write だけが失敗する状況を、タイミングウィンドウなしで安定して
	// 作るために使う）。
	alwaysFailWriteErr error
}

func newMockTransport(name string) *mockTransport {
	return &mockTransport{
		readCh:  make(chan []byte, 100),
		writeCh: make(chan []byte, 100),
		closeCh: make(chan struct{}),
		name:    transport.Name(name),
	}
}

func (m *mockTransport) Read() ([]byte, error) {
	select {
	case data := <-m.readCh:
		return data, nil
	case <-m.closeCh:
		return nil, errors.New("transport closed")
	}
}

func (m *mockTransport) Write(bs []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.alwaysFailWriteErr != nil {
		return m.alwaysFailWriteErr
	}
	if m.isClosed {
		if m.writeErrWhenClosed != nil {
			return m.writeErrWhenClosed
		}
		return errors.New("transport closed")
	}
	m.writeCh <- bs
	return nil
}

// SetAlwaysFailWrite は、以後の Write 呼び出しを常に err で失敗させる。
func (m *mockTransport) SetAlwaysFailWrite(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.alwaysFailWriteErr = err
}

func (m *mockTransport) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.isClosed {
		m.isClosed = true
		close(m.closeCh)
	}
	return nil
}

func (m *mockTransport) CloseWithStatus(_ transport.CloseStatus) error {
	return m.Close()
}

func (m *mockTransport) IsClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.isClosed
}

func (m *mockTransport) Name() transport.Name {
	return m.name
}

func (m *mockTransport) NegotiationParams() transport.NegotiationParams {
	return transport.NegotiationParams{}
}

func (m *mockTransport) AsUnreliable() (transport.UnreliableTransport, bool) {
	return nil, false
}

func (m *mockTransport) RxBytesCounterValue() uint64 { return 0 }
func (m *mockTransport) TxBytesCounterValue() uint64 { return 0 }

// ---------- Mock Transport Selector ----------

// mockTransportSelector は TransportSelector と MultiTransportSetter を実装するモック。
type mockTransportSelector struct {
	mu             sync.Mutex
	selected       transport.SubConnectionID
	multiTransport *Transport
}

func newMockTransportSelector(selected transport.SubConnectionID) *mockTransportSelector {
	return &mockTransportSelector{selected: selected}
}

func (s *mockTransportSelector) Get(_ context.Context, bsSize int64) transport.SubConnectionID {
	s.mu.Lock()
	id := s.selected
	mt := s.multiTransport
	s.mu.Unlock()

	if mt != nil {
		return SelectAvailableTransport(id, mt.Transports())
	}
	return id
}

func (s *mockTransportSelector) SetMultiTransport(mt *Transport) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.multiTransport = mt
}

// ---------- Helpers ----------

const testSuperConnectionID = "test-super-connection"

// newTestReconnectTransport はテスト用の reconnect.Transport を作成する。
// 常に同じ mockTransport を返すダイアラーを使用する。
func newTestReconnectTransport(t *testing.T, mock *mockTransport, subConnID string) *reconnect.Transport {
	t.Helper()
	rt, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			return mock, nil
		}),
		DialConfig: transport.DialConfig{
			SubConnectionID:   transport.SubConnectionID(subConnID),
			SuperConnectionID: transport.SuperConnectionID(testSuperConnectionID),
			TransportType:     transport.NegotiationNameWebSocket, // v4 プロトコルを有効化
		},
		MaxReconnectAttempts: 1,
		ReconnectInterval:    10 * time.Millisecond,
		HeartbeatInterval:    1 * time.Hour,
		HeartbeatTimeout:     1 * time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)
	return rt
}

// newFailingReconnectTransport は初回接続のみ成功し、以後は常に失敗する reconnect.Transport を作成する。
// 再接続試行は無制限で、StatusReconnecting 状態を維持する。
func newFailingReconnectTransport(t *testing.T, mock *mockTransport, subConnID string) *reconnect.Transport {
	t.Helper()
	var mu sync.Mutex
	connected := false
	rt, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			mu.Lock()
			defer mu.Unlock()
			if !connected {
				connected = true
				return mock, nil
			}
			return nil, errors.New("connection refused")
		}),
		DialConfig: transport.DialConfig{
			SubConnectionID:   transport.SubConnectionID(subConnID),
			SuperConnectionID: transport.SuperConnectionID(testSuperConnectionID),
			TransportType:     transport.NegotiationNameWebSocket, // v4 プロトコルを有効化
		},
		MaxReconnectAttempts: -1, // 無制限リトライで Reconnecting 状態を維持
		ReconnectInterval:    10 * time.Millisecond,
		HeartbeatInterval:    1 * time.Hour,
		HeartbeatTimeout:     1 * time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)
	return rt
}

func waitForConnected(t *testing.T, rt *reconnect.Transport) {
	t.Helper()
	require.Eventually(t,
		func() bool { return rt.Status() == reconnect.StatusConnected },
		5*time.Second, 10*time.Millisecond,
		"reconnect transport should become Connected",
	)
}

// iscpMessage は iSCP メッセージフレーム (0x00 + data) を作成する。
func iscpMessage(data []byte) []byte {
	msg := make([]byte, len(data)+1)
	msg[0] = 0x00 // MessageTypeISCP
	copy(msg[1:], data)
	return msg
}

// closeAndWait は multi.Transport を Close し、ゴルーチンの終了を待つ。
func closeAndWait(t *testing.T, mt *Transport) {
	t.Helper()
	err := mt.Close()
	require.NoError(t, err)
	time.Sleep(200 * time.Millisecond)
}

// verifyNoGoroutineLeak は goleak.VerifyNone 相当だが、リトライ猶予を拡大する。
// defer で使うこと（goleak.VerifyNone(t) の代わりにこちらを使う）。
//
// go.uber.org/goleak@v1.3.0 は MaxSleep/MaxRetries に相当する公開オプションを
// 提供していない（options.go の maxSleep は非公開関数、opts.maxRetries を外部
// から変更する Option も存在しない）。デフォルトの合計リトライ猶予は約430ms
// （maxSleep=100ms, maxRetries=20 の指数バックオフ、_defaultRetries 参照）だが、
// 大量の goroutine が並行する高負荷下では reconnect.Transport の内部リーダー
// goroutine（tr.Read() が下層 Close の closeCh を検知して復帰し、readerDone が
// close され readLoop が return するまで）の終了がスケジューリング遅延の影響を
// 受け、この猶予を超えることがある。production 側（CloseWithStatus は下層
// トランスポートの Close を同期的に呼んでおり、Close を呼び忘れる経路は見当
// たらなかった）に問題がなくても goleak が誤検出しうる（2026-07-31 実測: 通常
// 実行で約10%、GOMAXPROCS=2 に絞ると約20%の頻度で transport/multi パッケージの
// goleak テストが一斉 FAIL するのを確認）。
//
// 公開されている goleak.Find を pollInterval 間隔でポーリングし、合計 totalBudget
// （約3.3秒）までリトライすることで、VerifyNone 相当の検査をリトライ猶予だけ
// 拡大して行う。リトライは Find が失敗した場合にしか走らないため、成功時の
// 実行時間には影響しない。
func verifyNoGoroutineLeak(t *testing.T) {
	t.Helper()
	const (
		pollInterval = 100 * time.Millisecond
		totalBudget  = 3300 * time.Millisecond
	)
	deadline := time.Now().Add(totalBudget)
	var err error
	for {
		if err = goleak.Find(); err == nil {
			return
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(pollInterval)
	}
	t.Error(err)
}

// ---------- Tests ----------

func TestNewMultiTransport(t *testing.T) {
	defer verifyNoGoroutineLeak(t)

	mock1 := newMockTransport("mock1")
	mock2 := newMockTransport("mock2")

	rt1 := newTestReconnectTransport(t, mock1, "sub1")
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
	assert.NotNil(t, mt)

	// Name に "multiple" が含まれることを確認
	name := string(mt.Name())
	assert.Contains(t, name, "multiple")

	// TransportMap に 2 つのトランスポートが含まれることを確認
	transports := mt.Transports()
	assert.Len(t, transports, 2)
	assert.Contains(t, transports, id1)
	assert.Contains(t, transports, id2)

	closeAndWait(t, mt)
}

func TestMultiTransport_ReadWrite(t *testing.T) {
	defer verifyNoGoroutineLeak(t)

	mock1 := newMockTransport("mock1")
	rt1 := newTestReconnectTransport(t, mock1, "sub1")
	waitForConnected(t, rt1)

	id1 := transport.SubConnectionID("transport1")
	selector := newMockTransportSelector(id1)

	mt, err := NewTransport(TransportConfig{
		TransportMap: TransportMap{
			id1: rt1,
		},
		TransportSelector:   selector,
		Logger:              log.NewNop(),
		StatusCheckInterval: 100 * time.Millisecond,
	})
	require.NoError(t, err)

	t.Run("write operation", func(t *testing.T) {
		testData := []byte("test data")
		err := mt.Write(testData)
		require.NoError(t, err)

		select {
		case received := <-mock1.writeCh:
			// reconnect.Transport は MessageTypeISCP (0x00) プレフィックスを付加する
			assert.Equal(t, iscpMessage(testData), received)
		case <-time.After(time.Second):
			t.Fatal("write timeout")
		}
	})

	t.Run("read operation", func(t *testing.T) {
		testData := []byte("test response")
		// reconnect.Transport の readLoop が期待するフォーマットでデータを送信
		mock1.readCh <- iscpMessage(testData)

		received, err := mt.Read()
		require.NoError(t, err)
		assert.Equal(t, testData, received)
	})

	closeAndWait(t, mt)
}

func TestMultiTransport_Write_Fallback(t *testing.T) {
	defer verifyNoGoroutineLeak(t)

	mock1 := newMockTransport("mock1")
	mock2 := newMockTransport("mock2")

	// transport1: 初回のみ接続成功、以後失敗（Reconnecting 状態を維持）
	rt1 := newFailingReconnectTransport(t, mock1, "sub1")
	// transport2: 常に接続成功
	rt2 := newTestReconnectTransport(t, mock2, "sub2")
	waitForConnected(t, rt1)
	waitForConnected(t, rt2)

	id1 := transport.SubConnectionID("transport1")
	id2 := transport.SubConnectionID("transport2")

	// transport1 を優先するセレクタ
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

	// transport1 の下層モックをクローズ（切断をシミュレート）
	mock1.Close()

	// transport1 が Reconnecting になるまで待機
	require.Eventually(t,
		func() bool { return rt1.Status() == reconnect.StatusReconnecting },
		5*time.Second, 10*time.Millisecond,
		"transport1 should become Reconnecting",
	)

	// Write - セレクタは transport1 を選択するが、SelectAvailableTransport が
	// transport2 にフォールバックする
	testData := []byte("fallback test data")
	err = mt.Write(testData)
	require.NoError(t, err)

	// transport2 にデータが書き込まれたことを確認
	select {
	case received := <-mock2.writeCh:
		assert.Equal(t, iscpMessage(testData), received)
	case <-time.After(time.Second):
		t.Fatal("write timeout on fallback transport")
	}

	// transport1 にはデータが書き込まれていないことを確認
	select {
	case <-mock1.writeCh:
		t.Fatal("data should not be written to disconnected transport")
	default:
		// expected
	}

	closeAndWait(t, mt)
}

func TestMultiTransport_Write_AllDisconnected(t *testing.T) {
	defer verifyNoGoroutineLeak(t)

	// 両方のダイアラーが常に失敗する（初回接続も失敗）
	rt1, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			return nil, errors.New("connection refused")
		}),
		DialConfig: transport.DialConfig{
			SubConnectionID:   "sub1",
			SuperConnectionID: transport.SuperConnectionID(testSuperConnectionID),
		},
		MaxReconnectAttempts: 1,
		ReconnectInterval:    10 * time.Millisecond,
		HeartbeatInterval:    1 * time.Hour,
		HeartbeatTimeout:     1 * time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)

	rt2, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			return nil, errors.New("connection refused")
		}),
		DialConfig: transport.DialConfig{
			SubConnectionID:   "sub2",
			SuperConnectionID: transport.SuperConnectionID(testSuperConnectionID),
		},
		MaxReconnectAttempts: 1,
		ReconnectInterval:    10 * time.Millisecond,
		HeartbeatInterval:    1 * time.Hour,
		HeartbeatTimeout:     1 * time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)

	// 両方が Disconnected になるまで待機
	require.Eventually(t,
		func() bool { return rt1.Status() == reconnect.StatusDisconnected },
		5*time.Second, 10*time.Millisecond,
		"transport1 should become Disconnected",
	)
	require.Eventually(t,
		func() bool { return rt2.Status() == reconnect.StatusDisconnected },
		5*time.Second, 10*time.Millisecond,
		"transport2 should become Disconnected",
	)

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

	// OverallStatus が Disconnected になるまで待機
	require.Eventually(t,
		func() bool { return mt.OverallStatus() == MultiOverallStatusDisconnected },
		5*time.Second, 10*time.Millisecond,
		"overall status should become Disconnected",
	)

	// Write は失敗すべき
	err = mt.Write([]byte("should fail"))
	assert.Error(t, err)

	mt.Close()
	time.Sleep(100 * time.Millisecond)
}

func TestMultiTransport_Close(t *testing.T) {
	defer verifyNoGoroutineLeak(t)

	mock1 := newMockTransport("mock1")
	mock2 := newMockTransport("mock2")

	rt1 := newTestReconnectTransport(t, mock1, "sub1")
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

	err = mt.Close()
	require.NoError(t, err)

	// 内部トランスポートが Disconnected になっていることを確認
	assert.Equal(t, reconnect.StatusDisconnected, rt1.Status())
	assert.Equal(t, reconnect.StatusDisconnected, rt2.Status())

	// モックトランスポートが閉じていることを確認
	assert.True(t, mock1.IsClosed())
	assert.True(t, mock2.IsClosed())

	time.Sleep(200 * time.Millisecond)
}

func TestMultiTransport_OverallStatus(t *testing.T) {
	defer verifyNoGoroutineLeak(t)

	mock1 := newMockTransport("mock1")
	mock2 := newMockTransport("mock2")

	rt1 := newTestReconnectTransport(t, mock1, "sub1")
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

	// 初期状態: AllConnected
	require.Eventually(t,
		func() bool { return mt.OverallStatus() == MultiOverallStatusAllConnected },
		5*time.Second, 10*time.Millisecond,
		"overall status should be AllConnected",
	)

	closeAndWait(t, mt)
}

func TestMultiTransport_SuperConnectionID_SubConnectionID(t *testing.T) {
	defer verifyNoGoroutineLeak(t)

	mock1 := newMockTransport("mock1")
	rt1 := newTestReconnectTransport(t, mock1, "my-sub-id")
	waitForConnected(t, rt1)

	id1 := transport.SubConnectionID("transport1")
	selector := newMockTransportSelector(id1)

	mt, err := NewTransport(TransportConfig{
		TransportMap: TransportMap{
			id1: rt1,
		},
		TransportSelector:   selector,
		Logger:              log.NewNop(),
		StatusCheckInterval: 100 * time.Millisecond,
	})
	require.NoError(t, err)

	// NegotiationParams の SuperConnectionID を確認
	params := mt.NegotiationParams()
	assert.Equal(t, transport.SuperConnectionID(testSuperConnectionID), params.SuperConnectionID)

	// NegotiationParams の SubConnectionID を確認
	assert.Equal(t, transport.SubConnectionID("my-sub-id"), params.SubConnectionID)

	// TransportMap のキーでアクセスできることを確認
	transports := mt.Transports()
	_, exists := transports[id1]
	assert.True(t, exists)

	closeAndWait(t, mt)
}
