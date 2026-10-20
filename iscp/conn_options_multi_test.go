package iscp_test

import (
	"errors"
	"testing"
	"time"

	. "github.com/aptpod/iscp-go/v2/iscp"
	"github.com/aptpod/iscp-go/v2/log"
	"github.com/aptpod/iscp-go/v2/transport"
	"github.com/aptpod/iscp-go/v2/transport/multi"
	"github.com/stretchr/testify/require"
)

// failingDialer is a dialer that always fails. It keeps sub-connections in StatusConnecting.
type failingDialer struct{}

func (failingDialer) Dial(dc transport.DialConfig) (transport.Transport, error) {
	return nil, errors.New("test: dial always fails")
}

// TestCreateMultiTransport_ClosesWhenAllSubsStayDisconnectedPastThreshold verifies that
// the parent gives up on the SDK path (iscp.ConnConfig.createMultiTransport) as well.
//
// Note: this test does **not distinguish whether the wiring exists**. Without the wiring,
// MaxReconnectAttempts=3 is passed to each sub as is, the subs are exhausted after 3 attempts
// and fall to StatusDisconnected, and the same Disconnected state is reached via the other path
// "all subs are Disconnected" (the existing branch of updateOverallStatus), so this test passes
// even without the wiring.
// The threshold is MaxReconnectAttempts × ReconnectInterval, so both paths take about the same time.
//
// A regression of the wiring itself is guarded by TestCreateMultiTransport_PassesUnlimitedRetryToSubs
// (without the wiring, -1 is no longer passed to the subs and that test fails).
// This test only confirms that the connection is closed via the threshold path when the wiring exists.
func TestCreateMultiTransport_ClosesWhenAllSubsStayDisconnectedPastThreshold(t *testing.T) {
	c := &ConnConfig{
		Address: "127.0.0.1:1",
		Logger:  log.NewNop(),
		MultiTransportConfig: &MultiTransportConfig{
			DialerMap: map[transport.SubConnectionID]transport.Dialer{
				"sub1": failingDialer{},
				"sub2": failingDialer{},
			},
			// threshold = 3 × 50ms = 150ms
			MaxReconnectAttempts: 3,
			ReconnectInterval:    50 * time.Millisecond,
		},
	}

	tr, err := c.ExportCreateMultiTransport()
	require.NoError(t, err)
	t.Cleanup(func() { _ = tr.Close() })

	mt, ok := tr.(*multi.Transport)
	require.True(t, ok)

	require.Eventually(t,
		func() bool { return mt.OverallStatus() == multi.MultiOverallStatusDisconnected },
		5*time.Second, 20*time.Millisecond,
		"expected Disconnected after exceeding the threshold on the SDK path as well")
}

// TestCreateMultiTransport_DoesNotCloseWithUnlimitedRetry is the SDK-path version of the unlimited-retry case.
func TestCreateMultiTransport_DoesNotCloseWithUnlimitedRetry(t *testing.T) {
	c := &ConnConfig{
		Address: "127.0.0.1:1",
		Logger:  log.NewNop(),
		MultiTransportConfig: &MultiTransportConfig{
			DialerMap: map[transport.SubConnectionID]transport.Dialer{
				"sub1": failingDialer{},
				"sub2": failingDialer{},
			},
			MaxReconnectAttempts: -1,
			ReconnectInterval:    50 * time.Millisecond,
		},
	}

	tr, err := c.ExportCreateMultiTransport()
	require.NoError(t, err)
	t.Cleanup(func() { _ = tr.Close() })

	mt, ok := tr.(*multi.Transport)
	require.True(t, ok)

	require.Never(t,
		func() bool { return mt.OverallStatus() == multi.MultiOverallStatusDisconnected },
		1*time.Second, 20*time.Millisecond,
		"must not close with MaxReconnectAttempts=-1 (backward compatible)")
}

// TestCreateMultiTransport_PassesUnlimitedRetryToSubs verifies that an individual
// sub-connection never exhausts its retries and becomes unrecoverable.
func TestCreateMultiTransport_PassesUnlimitedRetryToSubs(t *testing.T) {
	c := &ConnConfig{
		Address: "127.0.0.1:1",
		Logger:  log.NewNop(),
		MultiTransportConfig: &MultiTransportConfig{
			DialerMap: map[transport.SubConnectionID]transport.Dialer{
				"sub1": failingDialer{},
			},
			MaxReconnectAttempts: 2,
			ReconnectInterval:    50 * time.Millisecond,
		},
	}

	tr, err := c.ExportCreateMultiTransport()
	require.NoError(t, err)
	t.Cleanup(func() { _ = tr.Close() })

	mt, ok := tr.(*multi.Transport)
	require.True(t, ok)

	for id, sub := range mt.Transports() {
		got := sub.NegotiationParams().MaxReconnectAttempts
		require.NotNil(t, got, "failed to get negotiation params of sub %s", id)
		require.Equal(t, -1, *got,
			"sub %s should always receive -1 (unlimited). The parent decides liveness of the whole", id)
	}
}
