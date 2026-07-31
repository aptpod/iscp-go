package nic_test

import (
	"context"
	"net"
	"testing"

	"github.com/aptpod/iscp-go/v2/transport/nic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDialContext_空のNIC名はエラー(t *testing.T) {
	_, err := nic.NewDialContext(nic.DialContextConfig{NIC: ""})
	assert.Error(t, err)
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
	assert.ErrorContains(t, err, "mws-not-exist0")
}

func TestSelectIPv4(t *testing.T) {
	tests := []struct {
		name      string
		addrs     []net.Addr
		wantIP    string
		wantFound bool
	}{
		{
			name: "loopbackは除外する",
			addrs: []net.Addr{
				&net.IPNet{IP: net.ParseIP("127.0.0.1"), Mask: net.CIDRMask(8, 32)},
			},
		},
		{
			name: "IPv4のlink-localは除外する",
			addrs: []net.Addr{
				&net.IPNet{IP: net.ParseIP("169.254.10.20"), Mask: net.CIDRMask(16, 32)},
			},
		},
		{
			name: "IPv6のlink-localは除外する",
			addrs: []net.Addr{
				&net.IPNet{IP: net.ParseIP("fe80::1"), Mask: net.CIDRMask(64, 128)},
			},
		},
		{
			name: "IPv4とIPv6が混在するとIPv4を選ぶ",
			addrs: []net.Addr{
				&net.IPNet{IP: net.ParseIP("2001:db8::1"), Mask: net.CIDRMask(64, 128)},
				&net.IPNet{IP: net.ParseIP("198.18.10.1"), Mask: net.CIDRMask(24, 32)},
			},
			wantIP:    "198.18.10.1",
			wantFound: true,
		},
		{
			name: "複数のIPv4は先頭を選ぶ",
			addrs: []net.Addr{
				&net.IPNet{IP: net.ParseIP("198.18.10.1"), Mask: net.CIDRMask(24, 32)},
				&net.IPNet{IP: net.ParseIP("198.18.10.2"), Mask: net.CIDRMask(24, 32)},
			},
			wantIP:    "198.18.10.1",
			wantFound: true,
		},
		{
			name: "候補が無ければ見つからない",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := nic.SelectIPv4(tt.addrs)
			assert.Equal(t, tt.wantFound, found)
			if !tt.wantFound {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, tt.wantIP, got.IP.String())
		})
	}
}
