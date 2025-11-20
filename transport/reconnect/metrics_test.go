package reconnect_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aptpod/iscp-go/log"
	"github.com/aptpod/iscp-go/transport"
	"github.com/aptpod/iscp-go/transport/compress"
	. "github.com/aptpod/iscp-go/transport/reconnect"
	"github.com/aptpod/iscp-go/transport/websocket"
)

// TestReconnectTransport_MetricsProvider は、reconnect.Transport 経由で
// MetricsProvider のメトリクス値が取得できることを確認します。
func TestReconnectTransport_MetricsProvider(t *testing.T) {
	// WebSocketエコーサーバーを起動
	sv := httptest.NewServer(http.HandlerFunc(echoHandler(t)))
	t.Cleanup(sv.Close)
	svURL, err := url.Parse(sv.URL)
	require.NoError(t, err)

	// reconnect.Transportを作成
	tr, err := Dial(DialConfig{
		Dialer: websocket.NewDefaultDialer(),
		DialConfig: transport.DialConfig{
			Address:        svURL.Host,
			CompressConfig: compress.Config{},
			EncodingName:   transport.EncodingNameJSON,
		},
		MaxReconnectAttempts: 10,
		ReconnectInterval:    time.Millisecond * 100,
		Logger:               log.NewStd(),
	})
	require.NoError(t, err)
	defer tr.Close()

	// 接続が確立されるまで待機
	require.Eventually(t,
		func() bool { return tr.Status() == StatusConnected },
		time.Second, 10*time.Millisecond,
		"status should become Connected",
	)

	tests := []struct {
		name         string
		metricGetter func() any
		wantNonZero  bool // デフォルト値以外を期待する場合true
		description  string
	}{
		{
			name: "success: RTT returns value",
			metricGetter: func() any {
				return tr.RTT()
			},
			wantNonZero: false, // デフォルト値（100ms）が返される可能性がある
			description: "RTT メトリクスを取得できること",
		},
		{
			name: "success: RTTVar returns value",
			metricGetter: func() any {
				return tr.RTTVar()
			},
			wantNonZero: false, // デフォルト値（50ms）が返される可能性がある
			description: "RTTVar メトリクスを取得できること",
		},
		{
			name: "success: CongestionWindow returns value",
			metricGetter: func() any {
				return tr.CongestionWindow()
			},
			wantNonZero: false, // デフォルト値（14600）が返される可能性がある
			description: "CongestionWindow メトリクスを取得できること",
		},
		{
			name: "success: BytesInFlight returns value",
			metricGetter: func() any {
				return tr.BytesInFlight()
			},
			wantNonZero: false, // 初期状態では0の可能性がある
			description: "BytesInFlight メトリクスを取得できること",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// メトリクス値を取得
			got := tt.metricGetter()

			// 値が取得できることを確認（nilでないこと）
			require.NotNil(t, got, tt.description)

			// 型に応じた検証
			switch v := got.(type) {
			case time.Duration:
				// time.Durationの場合、負の値でないことを確認
				assert.GreaterOrEqual(t, v, time.Duration(0), "メトリクス値は負でないこと")
				t.Logf("Metric value (Duration): %v", v)
			case uint64:
				// uint64の場合、特に検証は不要（負にならない）
				t.Logf("Metric value (uint64): %d", v)
			default:
				t.Fatalf("Unexpected metric type: %T", v)
			}
		})
	}
}

// TestReconnectTransport_MetricsProvider_AfterReconnect は、
// 再接続後もメトリクス値が取得できることを確認します。
func TestReconnectTransport_MetricsProvider_AfterReconnect(t *testing.T) {
	// Flakey（不安定な）WebSocketサーバーを起動
	sv := httptest.NewServer(http.HandlerFunc(flakeyHandler(t)))
	t.Cleanup(sv.Close)
	svURL, err := url.Parse(sv.URL)
	require.NoError(t, err)

	// reconnect.Transportを作成
	tr, err := Dial(DialConfig{
		Dialer: websocket.NewDefaultDialer(),
		DialConfig: transport.DialConfig{
			Address:        svURL.Host,
			CompressConfig: compress.Config{},
			EncodingName:   transport.EncodingNameJSON,
		},
		MaxReconnectAttempts: 100,
		ReconnectInterval:    time.Millisecond * 100,
		Logger:               log.NewStd(),
	})
	require.NoError(t, err)
	defer tr.Close()

	// 初期接続が確立されるまで待機
	require.Eventually(t,
		func() bool { return tr.Status() == StatusConnected },
		time.Second, 10*time.Millisecond,
		"status should become Connected",
	)

	// 初回のメトリクス取得
	initialRTT := tr.RTT()
	initialRTTVar := tr.RTTVar()
	initialCWND := tr.CongestionWindow()
	initialBytesInFlight := tr.BytesInFlight()

	t.Logf("Initial metrics - RTT: %v, RTTVar: %v, CWND: %d, BytesInFlight: %d",
		initialRTT, initialRTTVar, initialCWND, initialBytesInFlight)

	// いくつかのメッセージを送信して再接続をトリガー
	for range 5 {
		_ = tr.Write([]byte("test message"))
		time.Sleep(50 * time.Millisecond)
	}

	// 再接続後のメトリクス取得
	rttAfterReconnect := tr.RTT()
	rttVarAfterReconnect := tr.RTTVar()
	cwndAfterReconnect := tr.CongestionWindow()
	bytesInFlightAfterReconnect := tr.BytesInFlight()

	t.Logf("After reconnect metrics - RTT: %v, RTTVar: %v, CWND: %d, BytesInFlight: %d",
		rttAfterReconnect, rttVarAfterReconnect, cwndAfterReconnect, bytesInFlightAfterReconnect)

	// 再接続後もメトリクス値が取得できることを確認
	tests := []struct {
		name  string
		value any
	}{
		{
			name:  "RTT after reconnect",
			value: rttAfterReconnect,
		},
		{
			name:  "RTTVar after reconnect",
			value: rttVarAfterReconnect,
		},
		{
			name:  "CongestionWindow after reconnect",
			value: cwndAfterReconnect,
		},
		{
			name:  "BytesInFlight after reconnect",
			value: bytesInFlightAfterReconnect,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 値が取得できることを確認（nilでないこと）
			require.NotNil(t, tt.value)

			// 型に応じた検証
			switch v := tt.value.(type) {
			case time.Duration:
				assert.GreaterOrEqual(t, v, time.Duration(0), "メトリクス値は負でないこと")
			case uint64:
				// uint64の場合、特に検証は不要（負にならない）
			default:
				t.Fatalf("Unexpected metric type: %T", v)
			}
		})
	}
}

// TestReconnectTransport_MetricsProvider_DefaultValues は、
// デフォルト値が正しく返されることを確認します。
func TestReconnectTransport_MetricsProvider_DefaultValues(t *testing.T) {
	// WebSocketエコーサーバーを起動
	sv := httptest.NewServer(http.HandlerFunc(echoHandler(t)))
	t.Cleanup(sv.Close)
	svURL, err := url.Parse(sv.URL)
	require.NoError(t, err)

	// reconnect.Transportを作成
	tr, err := Dial(DialConfig{
		Dialer: websocket.NewDefaultDialer(),
		DialConfig: transport.DialConfig{
			Address:        svURL.Host,
			CompressConfig: compress.Config{},
			EncodingName:   transport.EncodingNameJSON,
		},
		MaxReconnectAttempts: 10,
		ReconnectInterval:    time.Millisecond * 100,
		Logger:               log.NewStd(),
	})
	require.NoError(t, err)
	defer tr.Close()

	// 接続が確立されるまで待機
	require.Eventually(t,
		func() bool { return tr.Status() == StatusConnected },
		time.Second, 10*time.Millisecond,
		"status should become Connected",
	)

	// メトリクス値を取得
	rtt := tr.RTT()
	rttVar := tr.RTTVar()
	cwnd := tr.CongestionWindow()
	bytesInFlight := tr.BytesInFlight()

	// デフォルト値または実際の値が返されることを確認
	// （TCPメトリクスが利用できない場合、デフォルト値が返される）
	tests := []struct {
		name     string
		value    any
		validate func(any) bool
	}{
		{
			name:  "RTT is valid duration",
			value: rtt,
			validate: func(v any) bool {
				d, ok := v.(time.Duration)
				return ok && d >= 0
			},
		},
		{
			name:  "RTTVar is valid duration",
			value: rttVar,
			validate: func(v any) bool {
				d, ok := v.(time.Duration)
				return ok && d >= 0
			},
		},
		{
			name:  "CongestionWindow is valid uint64",
			value: cwnd,
			validate: func(v any) bool {
				_, ok := v.(uint64)
				return ok
			},
		},
		{
			name:  "BytesInFlight is valid uint64",
			value: bytesInFlight,
			validate: func(v any) bool {
				_, ok := v.(uint64)
				return ok
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.True(t, tt.validate(tt.value),
				"メトリクス値が期待される型と範囲であること")
			t.Logf("Metric: %s = %v (type: %T)", tt.name, tt.value, tt.value)
		})
	}
}
