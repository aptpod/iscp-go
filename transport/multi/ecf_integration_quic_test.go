package multi_test

import (
	"context"
	"io"
	"testing"
	"time"

	quicgo "github.com/quic-go/quic-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aptpod/iscp-go/v2/internal/testdata"
	"github.com/aptpod/iscp-go/v2/log"
	"github.com/aptpod/iscp-go/v2/transport"
	"github.com/aptpod/iscp-go/v2/transport/compress"
	. "github.com/aptpod/iscp-go/v2/transport/multi"
	quictransport "github.com/aptpod/iscp-go/v2/transport/quic"
	"github.com/aptpod/iscp-go/v2/transport/reconnect"
)

// startQUICEchoServer starts a local QUIC echo server for the ECF integration tests.
func startQUICEchoServer(t testing.TB) (string, func()) {
	t.Helper()
	tlsConfig := testdata.GetTLSConfig()
	tlsConfig.NextProtos = []string{"iscp"}
	lis, err := quicgo.ListenAddr("localhost:0", tlsConfig, &quicgo.Config{
		EnableDatagrams:                  true,
		EnableStreamResetPartialDelivery: true,
	})
	require.NoError(t, err)

	ctx := context.Background()
	go func() {
		for {
			sess, err := lis.Accept(ctx)
			if err != nil {
				return
			}
			go func() {
				defer sess.CloseWithError(0, "")
				for {
					recvStream, err := sess.AcceptUniStream(ctx)
					if err != nil {
						return
					}
					sendStream, err := sess.OpenUniStream()
					if err != nil {
						return
					}
					if _, err := io.Copy(sendStream, recvStream); err != nil {
						return
					}
					if err := sendStream.Close(); err != nil {
						return
					}
				}
			}()
		}
	}()
	return lis.Addr().String(), func() { lis.Close() }
}

func newQUICDialer() *quictransport.Dialer {
	tlsConfig := testdata.GetTLSConfig()
	tlsConfig.NextProtos = []string{"iscp"}
	return quictransport.NewDialer(quictransport.DialerConfig{
		TLSConfig: tlsConfig,
	})
}

// TestECFSelectorIntegration_QUIC_MetricsUpdateLoop is TestECFSelectorIntegration_MetricsUpdateLoop
// (WebSocket version) with only the transport swapped to QUIC. It verifies that the ECFSelector metrics update loop
// works correctly over QUIC.
func TestECFSelectorIntegration_QUIC_MetricsUpdateLoop(t *testing.T) {
	addr, closeFn := startQUICEchoServer(t)
	t.Cleanup(closeFn)

	tr, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: newQUICDialer(),
		DialConfig: transport.DialConfig{
			Address:           addr,
			CompressConfig:    compress.Config{},
			EncodingName:      transport.EncodingNameJSON,
			SuperConnectionID: "test-group",
		},
		MaxReconnectAttempts: 10,
		ReconnectInterval:    time.Millisecond * 100,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { tr.Close() })

	require.Eventually(t,
		func() bool { return tr.Status() == reconnect.StatusConnected },
		time.Second, 10*time.Millisecond,
		"status should become Connected",
	)

	selector := NewECFSelector()
	mt, err := NewTransport(TransportConfig{
		TransportMap: TransportMap{
			"quic1": tr,
		},
		TransportSelector: selector,
		Logger:            log.NewNop(),
	})
	require.NoError(t, err)
	defer mt.Close()

	// Wait for the metrics update loop to run
	time.Sleep(150 * time.Millisecond)

	// Call Get() and verify that the QUIC transport is selected
	selectedID := selector.Get(context.Background(), 1000)
	assert.Equal(t, transport.SubConnectionID("quic1"), selectedID)
}
