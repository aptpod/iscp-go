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

// failingDialer は常に失敗するダイアラー。sub-connection を StatusConnecting のまま固定する。
type failingDialer struct{}

func (failingDialer) Dial(dc transport.DialConfig) (transport.Transport, error) {
	return nil, errors.New("test: dial always fails")
}

// TestCreateMultiTransport_全sub未接続が閾値を超えたら畳まれる は spec の受入基準 8 を検証する。
// SDK 経路（iscp.ConnConfig.createMultiTransport）でも親による give-up が働くこと。
func TestCreateMultiTransport_全sub未接続が閾値を超えたら畳まれる(t *testing.T) {
	c := &ConnConfig{
		Address: "127.0.0.1:1",
		Logger:  log.NewNop(),
		MultiTransportConfig: &MultiTransportConfig{
			DialerMap: map[transport.SubConnectionID]transport.Dialer{
				"sub1": failingDialer{},
				"sub2": failingDialer{},
			},
			// 閾値 = 3 × 50ms = 150ms
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
		"SDK 経路でも閾値超過で Disconnected になるはず")
}

// TestCreateMultiTransport_無期限設定では畳まれない は spec の受入基準 5 の SDK 版。
func TestCreateMultiTransport_無期限設定では畳まれない(t *testing.T) {
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
		"MaxReconnectAttempts=-1 では畳まない（現行互換）")
}

// TestCreateMultiTransport_subには常に無期限リトライを渡す は spec の設計 1 と
// 受入基準 6 を検証する。個別の sub-connection がリトライを使い切って
// 復帰不能になることがないこと。
func TestCreateMultiTransport_subには常に無期限リトライを渡す(t *testing.T) {
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
		require.NotNil(t, got, "sub %s のネゴシエーションパラメータが取れない", id)
		require.Equal(t, -1, *got,
			"sub %s には常に -1（無期限）が渡るはず。全体の生死判定は親が持つ", id)
	}
}
