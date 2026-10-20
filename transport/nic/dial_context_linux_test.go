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

// findNIC finds a non-loopback interface that is up and has an IPv4 address.
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
	t.Skip("no interface with an IPv4 address")
	return "", nil
}

// boundDevice returns the name of the interface that the socket of conn is bound to.
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

// dialToNIC listens on the IP of the interface and connects to it with dc.
// A bound socket reaches the IP of its own interface, but not 127.0.0.1.
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

func TestDialContext_BindsToNIC(t *testing.T) {
	t.Parallel()
	name, ip := findNIC(t)
	dc, err := nic.NewDialContext(nic.DialContextConfig{NIC: name})
	require.NoError(t, err)

	conn, err := dialToNIC(t, dc, ip)
	require.NoError(t, err)
	assert.Equal(t, name, boundDevice(t, conn))
	assert.Equal(t, ip.String(), conn.LocalAddr().(*net.TCPAddr).IP.String())
}

func TestDialContext_FallsBackToSourceIPWithoutBindPermission(t *testing.T) {
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

func TestDialContext_FailsOnBindErrorOtherThanPermission(t *testing.T) {
	t.Parallel()
	name, ip := findNIC(t)
	dc, err := nic.NewDialContext(nic.DialContextConfig{NIC: name})
	require.NoError(t, err)
	nic.SetBindToDevice(dc, func(int, string) error { return unix.ENODEV })

	_, err = dialToNIC(t, dc, ip)
	assert.ErrorIs(t, err, unix.ENODEV)
	assert.ErrorContains(t, err, "bind to device "+name)
}

func TestDialContext_UsesOnlySourceIPWhenBindDisabled(t *testing.T) {
	t.Parallel()
	name, ip := findNIC(t)
	dc, err := nic.NewDialContext(nic.DialContextConfig{NIC: name, DisableBindToDevice: true})
	require.NoError(t, err)

	conn, err := dialToNIC(t, dc, ip)
	require.NoError(t, err)
	assert.Empty(t, boundDevice(t, conn))
	assert.Equal(t, ip.String(), conn.LocalAddr().(*net.TCPAddr).IP.String())
}
