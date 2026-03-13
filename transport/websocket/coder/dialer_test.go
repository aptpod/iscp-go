package coder_test

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aptpod/iscp-go/transport/websocket"
	"github.com/aptpod/iscp-go/transport/websocket/coder"
)

// TestDialConfig_UnderlyingConn は、DialConfigで作成した接続が必ずUnderlyingConnを持つことを確認します。
func TestDialConfig_UnderlyingConn(t *testing.T) {
	url, cleanup := startEchoServer(t)
	t.Cleanup(cleanup)

	tests := []struct {
		name   string
		config websocket.DialConfig
	}{
		{
			name: "success: default config (no EnableMultipathTCP, no DialContext)",
			config: websocket.DialConfig{
				URL: url,
			},
		},
		{
			name: "success: with EnableMultipathTCP",
			config: websocket.DialConfig{
				URL:                url,
				EnableMultipathTCP: true,
			},
		},
		{
			name: "success: with custom DialContext",
			config: websocket.DialConfig{
				URL: url,
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					dialer := &net.Dialer{}
					return dialer.DialContext(ctx, network, addr)
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// DialConfigを呼び出してWebSocket接続を確立
			conn, err := coder.DialConfig(tt.config)
			require.NoError(t, err, "DialConfig should succeed")
			defer conn.Close()

			// UnderlyingConnを取得（必ず非nilであることを確認）
			underlyingConn := conn.UnderlyingConn()
			require.NotNil(t, underlyingConn, "UnderlyingConn must not be nil")

			// 型が*net.TCPConnであることを確認
			tcpConn, ok := underlyingConn.(*net.TCPConn)
			assert.True(t, ok, "UnderlyingConn should be *net.TCPConn")
			assert.NotNil(t, tcpConn, "TCPConn should not be nil")
		})
	}
}
