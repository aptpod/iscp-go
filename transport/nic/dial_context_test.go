package nic_test

import (
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
