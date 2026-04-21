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

// startQUICEchoServer は、ECF 統合テスト用のローカル QUIC エコーサーバを起動します。
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

// TestECFSelectorIntegration_QUIC_MetricsUpdateLoop は、TestECFSelectorIntegration_MetricsUpdateLoop
// (WebSocket 版) をトランスポートだけ QUIC に差し替えたテスト。ECFSelector のメトリクス更新ループが
// QUIC 経由で正常に動作することを確認します。
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

	// メトリクス更新ループが動作するのを待機
	time.Sleep(150 * time.Millisecond)

	// Get()を呼び出して、QUIC トランスポートが選択されることを確認
	selectedID := selector.Get(context.Background(), 1000)
	assert.Equal(t, transport.SubConnectionID("quic1"), selectedID)
}
