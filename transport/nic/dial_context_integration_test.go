//go:build integration

package nic_test

import (
	"context"
	"net"
	"os/exec"
	"testing"

	"github.com/aptpod/iscp-go/v2/transport/nic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func run(t *testing.T, args ...string) {
	t.Helper()
	out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
	require.NoErrorf(t, err, "%v: %s", args, out)
}

// setupVeth は veth ペアを作り、片側に addr を張る。
func setupVeth(t *testing.T, name, peer, addr string) {
	t.Helper()
	_ = exec.Command("ip", "link", "del", name).Run()
	run(t, "ip", "link", "add", name, "type", "veth", "peer", "name", peer)
	t.Cleanup(func() {
		_ = exec.Command("ip", "link", "del", name).Run()
	})
	run(t, "ip", "addr", "add", addr, "dev", name)
	run(t, "ip", "link", "set", name, "up")
	run(t, "ip", "link", "set", peer, "up")
}

func TestDialContext_IP変化に追従する(t *testing.T) {
	const nicName = "mwsveth0"
	setupVeth(t, nicName, "mwsveth0p", "198.18.10.1/24")

	dc, err := nic.NewDialContext(nic.DialContextConfig{NIC: nicName})
	require.NoError(t, err)

	// 1 回目: 198.18.10.1 に bind されることを確認する。
	// dial 先を loopback にして、LocalAddr を指定しない場合と判別できるようにする。
	ln1, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	conn, err := dc.DialContext(context.Background(), "tcp", ln1.Addr().String())
	require.NoError(t, err)
	assert.Equal(t, "198.18.10.1", conn.LocalAddr().(*net.TCPAddr).IP.String())
	require.NoError(t, conn.Close())
	require.NoError(t, ln1.Close())

	// IP を張り替える。本体の再起動はしない。
	run(t, "ip", "addr", "del", "198.18.10.1/24", "dev", nicName)
	run(t, "ip", "addr", "add", "198.18.11.1/24", "dev", nicName)

	ln2, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln2.Close()

	conn2, err := dc.DialContext(context.Background(), "tcp", ln2.Addr().String())
	require.NoError(t, err)
	assert.Equal(t, "198.18.11.1", conn2.LocalAddr().(*net.TCPAddr).IP.String())
	require.NoError(t, conn2.Close())
}

func TestDialContext_linklocalのみのNICはエラーになる(t *testing.T) {
	const nicName = "mwsveth1"
	setupVeth(t, nicName, "mwsveth1p", "169.254.7.1/16")

	dc, err := nic.NewDialContext(nic.DialContextConfig{NIC: nicName})
	require.NoError(t, err)

	_, err = dc.DialContext(context.Background(), "tcp", "127.0.0.1:1")
	assert.ErrorContains(t, err, "no valid IPv4 address found")
}

func TestDialContext_後からIPが付くと使えるようになる(t *testing.T) {
	const nicName = "mwsveth2"
	_ = exec.Command("ip", "link", "del", nicName).Run()
	run(t, "ip", "link", "add", nicName, "type", "veth", "peer", "name", "mwsveth2p")
	t.Cleanup(func() {
		_ = exec.Command("ip", "link", "del", nicName).Run()
	})
	run(t, "ip", "link", "set", nicName, "up")

	dc, err := nic.NewDialContext(nic.DialContextConfig{NIC: nicName})
	require.NoError(t, err)

	_, err = dc.DialContext(context.Background(), "tcp", "127.0.0.1:1")
	require.ErrorContains(t, err, "no valid IPv4 address found")

	run(t, "ip", "addr", "add", "198.18.12.1/24", "dev", nicName)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	conn, err := dc.DialContext(context.Background(), "tcp", ln.Addr().String())
	require.NoError(t, err)
	assert.Equal(t, "198.18.12.1", conn.LocalAddr().(*net.TCPAddr).IP.String())
	require.NoError(t, conn.Close())
}
