package nic_test

import (
	"context"
	"net"
	"testing"

	"github.com/aptpod/iscp-go/v2/transport/nic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// findNIC は up していて IPv4 を持つ非 loopback の NIC を探す。
func findNIC(t *testing.T) (string, net.IP) {
	t.Helper()
	ifaces, err := net.Interfaces()
	require.NoError(t, err)
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		require.NoError(t, err)
		if addr, ok := nic.SelectIPv4(addrs); ok {
			return iface.Name, addr.IP
		}
	}
	t.Skip("IPv4 を持つ NIC が無い")
	return "", nil
}

// boundDevice は conn のソケットが bind されている NIC 名を返す。
func boundDevice(t *testing.T, conn net.Conn) string {
	t.Helper()
	raw, err := conn.(*net.TCPConn).SyscallConn()
	require.NoError(t, err)
	var dev string
	var optErr error
	require.NoError(t, raw.Control(func(fd uintptr) {
		dev, optErr = unix.GetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE)
	}))
	require.NoError(t, optErr)
	return dev
}

// dialToNIC は NIC の IP で待ち受け、dc でそこへ接続する。
// bind したソケットは自 NIC の IP には届くが、127.0.0.1 には届かない。
func dialToNIC(t *testing.T, dc *nic.DialContext, ip net.IP) (net.Conn, error) {
	t.Helper()
	ln, err := net.Listen("tcp", net.JoinHostPort(ip.String(), "0"))
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })
	conn, err := dc.DialContext(context.Background(), "tcp", ln.Addr().String())
	if conn != nil {
		t.Cleanup(func() { conn.Close() })
	}
	return conn, err
}

func TestDialContext_NICにbindして接続する(t *testing.T) {
	t.Parallel()
	name, ip := findNIC(t)
	dc, err := nic.NewDialContext(nic.DialContextConfig{NIC: name})
	require.NoError(t, err)

	conn, err := dialToNIC(t, dc, ip)
	require.NoError(t, err)
	assert.Equal(t, name, boundDevice(t, conn))
	assert.Equal(t, ip.String(), conn.LocalAddr().(*net.TCPAddr).IP.String())
}

func TestDialContext_bindの権限が無ければ送信元IPだけで接続する(t *testing.T) {
	t.Parallel()
	name, ip := findNIC(t)
	dc, err := nic.NewDialContext(nic.DialContextConfig{NIC: name})
	require.NoError(t, err)
	nic.SetBindToDevice(dc, func(int, string) error { return unix.EPERM })

	conn, err := dialToNIC(t, dc, ip)
	require.NoError(t, err)
	assert.Empty(t, boundDevice(t, conn))
	assert.Equal(t, ip.String(), conn.LocalAddr().(*net.TCPAddr).IP.String())
}

func TestDialContext_権限以外のbind失敗はエラーになる(t *testing.T) {
	t.Parallel()
	name, ip := findNIC(t)
	dc, err := nic.NewDialContext(nic.DialContextConfig{NIC: name})
	require.NoError(t, err)
	nic.SetBindToDevice(dc, func(int, string) error { return unix.ENODEV })

	_, err = dialToNIC(t, dc, ip)
	assert.ErrorIs(t, err, unix.ENODEV)
	assert.ErrorContains(t, err, "bind to device "+name)
}
