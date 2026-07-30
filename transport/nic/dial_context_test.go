package nic_test

import (
	"context"
	"net"
	"testing"

	"github.com/aptpod/iscp-go/v2/transport/nic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// firstNonLoopbackIPv4NIC は IPv4 を持つ非 loopback インターフェース名を返す。
// 見つからない環境ではテストをスキップする。
func firstNonLoopbackIPv4NIC(t *testing.T) string {
	t.Helper()
	ifaces, err := net.Interfaces()
	require.NoError(t, err)
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok || ipNet.IP.To4() == nil {
				continue
			}
			return iface.Name
		}
	}
	t.Skip("IPv4 を持つ非 loopback インターフェースが無い環境のためスキップします")
	return ""
}

func TestNewDialContext_空のNIC名はエラー(t *testing.T) {
	_, err := nic.NewDialContext(nic.DialContextConfig{NIC: ""})
	assert.Error(t, err)
}

func TestNewDialContext_存在するNICなら成功する(t *testing.T) {
	name := firstNonLoopbackIPv4NIC(t)
	dc, err := nic.NewDialContext(nic.DialContextConfig{NIC: name})
	require.NoError(t, err)
	assert.NotNil(t, dc)
}

func TestNewDialContext_存在しないNICでも成功する(t *testing.T) {
	// 起動時に存在しない NIC（USB ドングルの挿抜、LTE モジュールの初期化遅延）を
	// 許すため、NewDialContext ではインターフェースの存在確認をしない。
	dc, err := nic.NewDialContext(nic.DialContextConfig{NIC: "mws-not-exist0"})
	require.NoError(t, err)
	assert.NotNil(t, dc)
}

func TestDialContext_存在しないNICはdial時にエラーになる(t *testing.T) {
	dc, err := nic.NewDialContext(nic.DialContextConfig{NIC: "mws-not-exist0"})
	require.NoError(t, err)

	_, err = dc.DialContext(context.Background(), "tcp", "127.0.0.1:1")
	assert.ErrorContains(t, err, "get local address")
}
