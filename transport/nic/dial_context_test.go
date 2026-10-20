package nic_test

import (
	"context"
	"net"
	"testing"

	"github.com/aptpod/iscp-go/v2/transport/nic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDialContext_FailsOnEmptyNICName(t *testing.T) {
	_, err := nic.NewDialContext(nic.DialContextConfig{NIC: ""})
	assert.Error(t, err)
}

func TestNewDialContext_SucceedsForMissingNIC(t *testing.T) {
	// NewDialContext does not check that the interface exists, to allow an interface
	// that is missing at startup (a USB dongle plugged in later, a slow LTE module).
	dc, err := nic.NewDialContext(nic.DialContextConfig{NIC: "mws-not-exist0"})
	require.NoError(t, err)
	assert.NotNil(t, dc)
}

func TestDialContext_FailsAtDialForMissingNIC(t *testing.T) {
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
			name: "skips loopback",
			addrs: []net.Addr{
				&net.IPNet{IP: net.ParseIP("127.0.0.1"), Mask: net.CIDRMask(8, 32)},
			},
		},
		{
			name: "skips IPv4 link-local",
			addrs: []net.Addr{
				&net.IPNet{IP: net.ParseIP("169.254.10.20"), Mask: net.CIDRMask(16, 32)},
			},
		},
		{
			name: "skips IPv6 link-local",
			addrs: []net.Addr{
				&net.IPNet{IP: net.ParseIP("fe80::1"), Mask: net.CIDRMask(64, 128)},
			},
		},
		{
			name: "prefers IPv4 over IPv6",
			addrs: []net.Addr{
				&net.IPNet{IP: net.ParseIP("2001:db8::1"), Mask: net.CIDRMask(64, 128)},
				&net.IPNet{IP: net.ParseIP("198.18.10.1"), Mask: net.CIDRMask(24, 32)},
			},
			wantIP:    "198.18.10.1",
			wantFound: true,
		},
		{
			name: "selects the first of multiple IPv4 addresses",
			addrs: []net.Addr{
				&net.IPNet{IP: net.ParseIP("198.18.10.1"), Mask: net.CIDRMask(24, 32)},
				&net.IPNet{IP: net.ParseIP("198.18.10.2"), Mask: net.CIDRMask(24, 32)},
			},
			wantIP:    "198.18.10.1",
			wantFound: true,
		},
		{
			name: "finds nothing without candidates",
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
