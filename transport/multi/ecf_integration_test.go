package multi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	cwebsocket "github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aptpod/iscp-go/log"
	"github.com/aptpod/iscp-go/transport"
	"github.com/aptpod/iscp-go/transport/compress"
	. "github.com/aptpod/iscp-go/transport/multi"
	"github.com/aptpod/iscp-go/transport/reconnect"
	"github.com/aptpod/iscp-go/transport/websocket"
	_ "github.com/aptpod/iscp-go/transport/websocket/coder"
)

// TestECFSelectorIntegration_NewTransport は、ECFSelector を使用して
// multi.Transport を初期化できることを確認します。
func TestECFSelectorIntegration_NewTransport(t *testing.T) {
	// WebSocketエコーサーバーを起動
	sv := httptest.NewServer(http.HandlerFunc(echoHandler(t)))
	t.Cleanup(sv.Close)
	svURL, err := url.Parse(sv.URL)
	require.NoError(t, err)

	// reconnect.Transportを作成
	tr, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: websocket.NewDefaultDialer(),
		DialConfig: transport.DialConfig{
			Address:          svURL.Host,
			CompressConfig:   compress.Config{},
			EncodingName:     transport.EncodingNameJSON,
			TransportGroupID: "test-group",
		},
		MaxReconnectAttempts: 10,
		ReconnectInterval:    time.Millisecond * 100,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { tr.Close() })

	// 接続が確立されるまで待機
	require.Eventually(t,
		func() bool { return tr.Status() == reconnect.StatusConnected },
		time.Second, 10*time.Millisecond,
		"status should become Connected",
	)

	// ECFSelectorを作成
	selector := NewECFSelector()

	// multi.Transportを作成
	transportMap := TransportMap{
		"transport1": tr,
	}

	mt, err := NewTransport(TransportConfig{
		TransportMap:      transportMap,
		TransportSelector: selector,
		Logger:            log.NewNop(),
	})
	require.NoError(t, err)
	defer mt.Close()

	// 初期化が正常に完了したことを確認
	assert.NotNil(t, mt)
}

// TestECFSelectorIntegration_MetricsUpdateLoop は、ECFSelector のメトリクス更新ループが
// 正常に動作することを確認します。
func TestECFSelectorIntegration_MetricsUpdateLoop(t *testing.T) {
	// WebSocketエコーサーバーを起動
	sv := httptest.NewServer(http.HandlerFunc(echoHandler(t)))
	t.Cleanup(sv.Close)
	svURL, err := url.Parse(sv.URL)
	require.NoError(t, err)

	// reconnect.Transportを作成
	tr, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: websocket.NewDefaultDialer(),
		DialConfig: transport.DialConfig{
			Address:          svURL.Host,
			CompressConfig:   compress.Config{},
			EncodingName:     transport.EncodingNameJSON,
			TransportGroupID: "test-group",
		},
		MaxReconnectAttempts: 10,
		ReconnectInterval:    time.Millisecond * 100,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { tr.Close() })

	// 接続が確立されるまで待機
	require.Eventually(t,
		func() bool { return tr.Status() == reconnect.StatusConnected },
		time.Second, 10*time.Millisecond,
		"status should become Connected",
	)

	// ECFSelectorを作成
	selector := NewECFSelector()

	// multi.Transportを作成
	transportMap := TransportMap{
		"transport1": tr,
	}

	mt, err := NewTransport(TransportConfig{
		TransportMap:      transportMap,
		TransportSelector: selector,
		Logger:            log.NewNop(),
	})
	require.NoError(t, err)
	defer mt.Close()

	// メトリクス更新ループが動作するのを待機
	time.Sleep(150 * time.Millisecond)

	// Get()を呼び出して、トランスポートが選択されることを確認
	selectedID := selector.Get(1000)
	assert.Equal(t, transport.TransportID("transport1"), selectedID)
}

// TestECFSelectorIntegration_MultipleTransports は、複数のトランスポートを持つ
// multi.Transport で ECFSelector が正常に動作することを確認します。
func TestECFSelectorIntegration_MultipleTransports(t *testing.T) {
	// 2つのWebSocketエコーサーバーを起動
	sv1 := httptest.NewServer(http.HandlerFunc(echoHandler(t)))
	t.Cleanup(sv1.Close)
	svURL1, err := url.Parse(sv1.URL)
	require.NoError(t, err)

	sv2 := httptest.NewServer(http.HandlerFunc(echoHandler(t)))
	t.Cleanup(sv2.Close)
	svURL2, err := url.Parse(sv2.URL)
	require.NoError(t, err)

	// 2つの reconnect.Transport を作成
	tr1, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: websocket.NewDefaultDialer(),
		DialConfig: transport.DialConfig{
			Address:          svURL1.Host,
			CompressConfig:   compress.Config{},
			EncodingName:     transport.EncodingNameJSON,
			TransportGroupID: "test-group",
		},
		MaxReconnectAttempts: 10,
		ReconnectInterval:    time.Millisecond * 100,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { tr1.Close() })

	tr2, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: websocket.NewDefaultDialer(),
		DialConfig: transport.DialConfig{
			Address:          svURL2.Host,
			CompressConfig:   compress.Config{},
			EncodingName:     transport.EncodingNameJSON,
			TransportGroupID: "test-group",
		},
		MaxReconnectAttempts: 10,
		ReconnectInterval:    time.Millisecond * 100,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { tr2.Close() })

	// 接続が確立されるまで待機
	require.Eventually(t,
		func() bool { return tr1.Status() == reconnect.StatusConnected },
		time.Second, 10*time.Millisecond,
		"transport1 status should become Connected",
	)
	require.Eventually(t,
		func() bool { return tr2.Status() == reconnect.StatusConnected },
		time.Second, 10*time.Millisecond,
		"transport2 status should become Connected",
	)

	// ECFSelectorを作成
	selector := NewECFSelector()

	// multi.Transportを作成
	transportMap := TransportMap{
		"transport1": tr1,
		"transport2": tr2,
	}

	mt, err := NewTransport(TransportConfig{
		TransportMap:      transportMap,
		TransportSelector: selector,
		Logger:            log.NewNop(),
	})
	require.NoError(t, err)
	defer mt.Close()

	// メトリクス更新ループが動作するのを待機
	time.Sleep(150 * time.Millisecond)

	// Get()を呼び出して、トランスポートが選択されることを確認
	selectedID := selector.Get(1000)
	assert.NotEqual(t, transport.TransportID(""), selectedID)
	assert.True(t, selectedID == "transport1" || selectedID == "transport2")
}

// TestECFSelectorIntegration_WriteWithSelector は、ECFSelector を使用して
// multi.Transport 経由でデータを書き込めることを確認します。
func TestECFSelectorIntegration_WriteWithSelector(t *testing.T) {
	// WebSocketエコーサーバーを起動
	sv := httptest.NewServer(http.HandlerFunc(echoHandler(t)))
	t.Cleanup(sv.Close)
	svURL, err := url.Parse(sv.URL)
	require.NoError(t, err)

	// reconnect.Transportを作成
	tr, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: websocket.NewDefaultDialer(),
		DialConfig: transport.DialConfig{
			Address:          svURL.Host,
			CompressConfig:   compress.Config{},
			EncodingName:     transport.EncodingNameJSON,
			TransportGroupID: "test-group",
		},
		MaxReconnectAttempts: 10,
		ReconnectInterval:    time.Millisecond * 100,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { tr.Close() })

	// 接続が確立されるまで待機
	require.Eventually(t,
		func() bool { return tr.Status() == reconnect.StatusConnected },
		time.Second, 10*time.Millisecond,
		"status should become Connected",
	)

	// ECFSelectorを作成
	selector := NewECFSelector()

	// multi.Transportを作成
	transportMap := TransportMap{
		"transport1": tr,
	}

	mt, err := NewTransport(TransportConfig{
		TransportMap:      transportMap,
		TransportSelector: selector,
		Logger:            log.NewNop(),
	})
	require.NoError(t, err)
	defer mt.Close()

	// メトリクス更新ループが動作するのを待機
	time.Sleep(150 * time.Millisecond)

	// データを書き込む
	testData := []byte("test message")
	err = mt.Write(testData)
	require.NoError(t, err)

	// エコーされたデータを読み取る
	received, err := mt.Read()
	require.NoError(t, err)
	assert.Equal(t, testData, received)
}

// TestECFSelectorIntegration_ConcurrentAccess は、ECFSelector を使用した
// multi.Transport への並行アクセスが安全であることを確認します。
func TestECFSelectorIntegration_ConcurrentAccess(t *testing.T) {
	// WebSocketエコーサーバーを起動
	sv := httptest.NewServer(http.HandlerFunc(echoHandler(t)))
	t.Cleanup(sv.Close)
	svURL, err := url.Parse(sv.URL)
	require.NoError(t, err)

	// reconnect.Transportを作成
	tr, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: websocket.NewDefaultDialer(),
		DialConfig: transport.DialConfig{
			Address:          svURL.Host,
			CompressConfig:   compress.Config{},
			EncodingName:     transport.EncodingNameJSON,
			TransportGroupID: "test-group",
		},
		MaxReconnectAttempts: 10,
		ReconnectInterval:    time.Millisecond * 100,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { tr.Close() })

	// 接続が確立されるまで待機
	require.Eventually(t,
		func() bool { return tr.Status() == reconnect.StatusConnected },
		time.Second, 10*time.Millisecond,
		"status should become Connected",
	)

	// ECFSelectorを作成
	selector := NewECFSelector()

	// multi.Transportを作成
	transportMap := TransportMap{
		"transport1": tr,
	}

	mt, err := NewTransport(TransportConfig{
		TransportMap:      transportMap,
		TransportSelector: selector,
		Logger:            log.NewNop(),
	})
	require.NoError(t, err)
	defer mt.Close()

	// メトリクス更新ループが動作するのを待機
	time.Sleep(150 * time.Millisecond)

	// 並行アクセスのテスト
	var wg sync.WaitGroup
	const numGoroutines = 10
	const numIterations = 10

	// 並行書き込み
	for range numGoroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range numIterations {
				_ = mt.Write([]byte("concurrent test"))
			}
		}()
	}

	// 並行セレクタアクセス
	for range numGoroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range numIterations {
				_ = selector.Get(1000)
			}
		}()
	}

	wg.Wait()
}

// TestECFSelectorIntegration_TransportMetricsUpdaterInterface は、
// ECFSelector が TransportMetricsUpdater インターフェースを実装していることを確認します。
func TestECFSelectorIntegration_TransportMetricsUpdaterInterface(t *testing.T) {
	selector := NewECFSelector()

	// TransportMetricsUpdater インターフェースを満たすことを確認
	var _ TransportMetricsUpdater = selector

	// UpdateTransport が呼び出せることを確認
	transportID := transport.TransportID("test-transport")
	provider := &mockMetricsProvider{
		rtt:              50 * time.Millisecond,
		rttvar:           25 * time.Millisecond,
		congestionWindow: 20000,
		bytesInFlight:    5000,
	}
	info := NewTransportInfo(transportID, provider)
	selector.UpdateTransport(transportID, info)

	// SetQueueSize が呼び出せることを確認
	selector.SetQueueSize(1000)

	// トランスポートが登録されていることを確認
	selectedID := selector.Get(1000)
	assert.Equal(t, transportID, selectedID)
}

// TestECFSelectorIntegration_MultiTransportSetterInterface は、
// ECFSelector が MultiTransportSetter インターフェースを実装していることを確認します。
func TestECFSelectorIntegration_MultiTransportSetterInterface(t *testing.T) {
	selector := NewECFSelector()

	// MultiTransportSetter インターフェースを満たすことを確認
	var _ MultiTransportSetter = selector

	// SetMultiTransport が呼び出せることを確認
	selector.SetMultiTransport(nil)
}

// echoHandler は WebSocket エコーサーバーのハンドラです。
func echoHandler(t testing.TB) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := cwebsocket.Accept(w, r, &cwebsocket.AcceptOptions{
			Subprotocols:         []string{},
			InsecureSkipVerify:   false,
			OriginPatterns:       []string{},
			CompressionMode:      0,
			CompressionThreshold: 0,
		})
		if err != nil {
			http.Error(w, "Failed to upgrade to websocket", http.StatusInternalServerError)
			return
		}
		defer conn.CloseNow()

		for {
			messageType, message, err := conn.Read(r.Context())
			if err != nil {
				break
			}
			t.Logf("messageType: %d, message: %s", messageType, string(message))
			err = conn.Write(context.Background(), messageType, message)
			if err != nil {
				break
			}
		}
	}
}
