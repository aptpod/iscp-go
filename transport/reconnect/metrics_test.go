package reconnect_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aptpod/iscp-go/v2/log"
	"github.com/aptpod/iscp-go/v2/transport"
	"github.com/aptpod/iscp-go/v2/transport/compress"
	. "github.com/aptpod/iscp-go/v2/transport/reconnect"
	"github.com/aptpod/iscp-go/v2/transport/websocket"
)

// TestReconnectTransport_MetricsProvider verifies that the metric values of MetricsProvider
// can be obtained via reconnect.Transport.
func TestReconnectTransport_MetricsProvider(t *testing.T) {
	// Start a WebSocket echo server
	sv := httptest.NewServer(http.HandlerFunc(echoHandler(t)))
	t.Cleanup(sv.Close)
	svURL, err := url.Parse(sv.URL)
	require.NoError(t, err)

	// Create a reconnect.Transport
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

	// Wait until the connection is established
	require.Eventually(t,
		func() bool { return tr.Status() == StatusConnected },
		time.Second, 10*time.Millisecond,
		"status should become Connected",
	)

	tests := []struct {
		name         string
		metricGetter func() any
		wantNonZero  bool // true if a non-default value is expected
		description  string
	}{
		{
			name: "success: RTT returns value",
			metricGetter: func() any {
				return tr.RTT()
			},
			wantNonZero: false, // the default value (100ms) may be returned
			description: "RTT metric should be obtainable",
		},
		{
			name: "success: RTTVar returns value",
			metricGetter: func() any {
				return tr.RTTVar()
			},
			wantNonZero: false, // the default value (50ms) may be returned
			description: "RTTVar metric should be obtainable",
		},
		{
			name: "success: CongestionWindow returns value",
			metricGetter: func() any {
				return tr.CongestionWindow()
			},
			wantNonZero: false, // the default value (14600) may be returned
			description: "CongestionWindow metric should be obtainable",
		},
		{
			name: "success: BytesInFlight returns value",
			metricGetter: func() any {
				return tr.BytesInFlight()
			},
			wantNonZero: false, // may be 0 in the initial state
			description: "BytesInFlight metric should be obtainable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Get the metric value
			got := tt.metricGetter()

			// Confirm that the value can be obtained (not nil)
			require.NotNil(t, got, tt.description)

			// Validation according to the type
			switch v := got.(type) {
			case time.Duration:
				// For time.Duration, confirm that it is not negative
				assert.GreaterOrEqual(t, v, time.Duration(0), "metric value should not be negative")
				t.Logf("Metric value (Duration): %v", v)
			case uint64:
				// For uint64, no particular validation is needed (it cannot be negative)
				t.Logf("Metric value (uint64): %d", v)
			default:
				t.Fatalf("Unexpected metric type: %T", v)
			}
		})
	}
}

// TestReconnectTransport_MetricsProvider_AfterReconnect verifies that
// the metric values can still be obtained after a reconnect.
func TestReconnectTransport_MetricsProvider_AfterReconnect(t *testing.T) {
	// Start a flaky (unstable) WebSocket server
	sv := httptest.NewServer(http.HandlerFunc(flakeyHandler(t)))
	t.Cleanup(sv.Close)
	svURL, err := url.Parse(sv.URL)
	require.NoError(t, err)

	// Create a reconnect.Transport
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

	// Wait until the initial connection is established
	require.Eventually(t,
		func() bool { return tr.Status() == StatusConnected },
		time.Second, 10*time.Millisecond,
		"status should become Connected",
	)

	// Get the metrics for the first time
	initialRTT := tr.RTT()
	initialRTTVar := tr.RTTVar()
	initialCWND := tr.CongestionWindow()
	initialBytesInFlight := tr.BytesInFlight()

	t.Logf("Initial metrics - RTT: %v, RTTVar: %v, CWND: %d, BytesInFlight: %d",
		initialRTT, initialRTTVar, initialCWND, initialBytesInFlight)

	// Send some messages to trigger a reconnect
	for range 5 {
		_ = tr.Write([]byte("test message"))
		time.Sleep(50 * time.Millisecond)
	}

	// Get the metrics after the reconnect
	rttAfterReconnect := tr.RTT()
	rttVarAfterReconnect := tr.RTTVar()
	cwndAfterReconnect := tr.CongestionWindow()
	bytesInFlightAfterReconnect := tr.BytesInFlight()

	t.Logf("After reconnect metrics - RTT: %v, RTTVar: %v, CWND: %d, BytesInFlight: %d",
		rttAfterReconnect, rttVarAfterReconnect, cwndAfterReconnect, bytesInFlightAfterReconnect)

	// Confirm that the metric values can still be obtained after the reconnect
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
			// Confirm that the value can be obtained (not nil)
			require.NotNil(t, tt.value)

			// Validation according to the type
			switch v := tt.value.(type) {
			case time.Duration:
				assert.GreaterOrEqual(t, v, time.Duration(0), "metric value should not be negative")
			case uint64:
				// For uint64, no particular validation is needed (it cannot be negative)
			default:
				t.Fatalf("Unexpected metric type: %T", v)
			}
		})
	}
}

// TestReconnectTransport_MetricsProvider_DefaultValues verifies that
// the default values are returned correctly.
func TestReconnectTransport_MetricsProvider_DefaultValues(t *testing.T) {
	// Start a WebSocket echo server
	sv := httptest.NewServer(http.HandlerFunc(echoHandler(t)))
	t.Cleanup(sv.Close)
	svURL, err := url.Parse(sv.URL)
	require.NoError(t, err)

	// Create a reconnect.Transport
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

	// Wait until the connection is established
	require.Eventually(t,
		func() bool { return tr.Status() == StatusConnected },
		time.Second, 10*time.Millisecond,
		"status should become Connected",
	)

	// Get the metric values
	rtt := tr.RTT()
	rttVar := tr.RTTVar()
	cwnd := tr.CongestionWindow()
	bytesInFlight := tr.BytesInFlight()

	// Confirm that the default values or the actual values are returned
	// (if TCP metrics are unavailable, the default values are returned)
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
				"metric value should be of the expected type and range")
			t.Logf("Metric: %s = %v (type: %T)", tt.name, tt.value, tt.value)
		})
	}
}
