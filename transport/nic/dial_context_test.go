package nic_test

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"

	. "github.com/aptpod/iscp-go/transport/nic"
)

func TestSelectIPv4(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		addrs   []net.Addr
		wantIP  net.IP
		wantErr bool
	}{
		{
			name: "正常なIPv4が選ばれる",
			addrs: []net.Addr{
				&net.IPNet{IP: net.ParseIP("192.168.1.10"), Mask: net.CIDRMask(24, 32)},
			},
			wantIP: net.ParseIP("192.168.1.10").To4(),
		},
		{
			name: "loopbackは選ばれない",
			addrs: []net.Addr{
				&net.IPNet{IP: net.ParseIP("127.0.0.1"), Mask: net.CIDRMask(8, 32)},
			},
			wantErr: true,
		},
		{
			name: "link-localしか無ければlink-localが選ばれる",
			addrs: []net.Addr{
				&net.IPNet{IP: net.ParseIP("169.254.1.1"), Mask: net.CIDRMask(16, 32)},
			},
			wantIP: net.ParseIP("169.254.1.1").To4(),
		},
		{
			name: "link-localより後ろにあってもroutableが優先される",
			addrs: []net.Addr{
				&net.IPNet{IP: net.ParseIP("169.254.1.1"), Mask: net.CIDRMask(16, 32)},
				&net.IPNet{IP: net.ParseIP("192.168.1.10"), Mask: net.CIDRMask(24, 32)},
			},
			wantIP: net.ParseIP("192.168.1.10").To4(),
		},
		{
			name: "IPv6のみなら選ばれずエラー",
			addrs: []net.Addr{
				&net.IPNet{IP: net.ParseIP("2001:db8::1"), Mask: net.CIDRMask(64, 128)},
			},
			wantErr: true,
		},
		{
			name:    "空ならエラー",
			addrs:   nil,
			wantErr: true,
		},
		{
			name: "loopbackを除きroutableなIPv4が優先される",
			addrs: []net.Addr{
				&net.IPNet{IP: net.ParseIP("127.0.0.1"), Mask: net.CIDRMask(8, 32)},
				&net.IPNet{IP: net.ParseIP("169.254.1.1"), Mask: net.CIDRMask(16, 32)},
				&net.IPNet{IP: net.ParseIP("10.0.0.5"), Mask: net.CIDRMask(24, 32)},
			},
			wantIP: net.ParseIP("10.0.0.5").To4(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := SelectIPv4(tt.addrs)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			if assert.NotNil(t, got) {
				assert.Equal(t, tt.wantIP, got.IP)
			}
		})
	}
}

func TestNewDialContext_NonExistentNIC(t *testing.T) {
	t.Parallel()

	const nicName = "nonexistent-nic-for-test"

	dc, err := NewDialContext(DialContextConfig{NIC: nicName})
	assert.NoError(t, err, "構築時点ではNICの存在確認をしないため成功する")
	assert.NotNil(t, dc)

	conn, err := dc.DialContext(context.Background(), "tcp", "127.0.0.1:0")
	assert.Nil(t, conn)
	if assert.Error(t, err) {
		assert.ErrorContains(t, err, nicName)
	}
}
