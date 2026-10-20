package nic

import (
	"context"
	"errors"
	"fmt"
	"net"
)

// DialContext dials connections that go out through a specific network interface.
//
// On Linux, the socket is bound to the interface with SO_BINDTODEVICE, so the
// outgoing interface does not depend on policy routing. The interface needs a
// route to the destination; otherwise dialing fails with an error such as
// "no route to host", or does not complete until the context deadline,
// depending on the interface type. Loopback addresses such as a local HTTP
// proxy (HTTPS_PROXY=http://127.0.0.1:...) are not reachable from a bound
// socket either. If binding is not permitted (EPERM), it falls back to setting
// only the source address.
// On other platforms, or when DisableBindToDevice is set, only the source
// address is set.
type DialContext struct {
	nic string
	// bindToDevice はテストで差し替えるために持つ。Linux 以外と bind しない設定では nil。
	bindToDevice func(fd int, nic string) error
}

type DialContextConfig struct {
	NIC string

	// DisableBindToDevice disables binding the socket to the interface and sets
	// only the source address. Use it when the outgoing interface is selected by
	// policy routing, e.g. for interfaces of a Docker bridge network.
	DisableBindToDevice bool
}

func NewDialContext(c DialContextConfig) (*DialContext, error) {
	if c.NIC == "" {
		return nil, errors.New("NIC is required")
	}
	// インターフェースの存在確認とアドレス解決はここでは行わない。
	// 起動時に存在しない NIC が後から現れる場合を許すため、解決は dial 時に行う。
	dc := &DialContext{nic: c.NIC}
	if !c.DisableBindToDevice {
		dc.bindToDevice = bindToDevice
	}
	return dc, nil
}

func (n *DialContext) DialContext(ctx context.Context, network string, address string) (net.Conn, error) {
	localAddr, err := getLocalAddrFromNIC(n.nic)
	if err != nil {
		return nil, fmt.Errorf("get local address for nic %s: %w", n.nic, err)
	}
	// bind が使えない環境でも送信元 IP は指定できるよう、LocalAddr は常に設定する。
	// ポリシールーティングのルールも送信元 IP で当たる。
	d := &net.Dialer{LocalAddr: localAddr}
	if n.bindToDevice != nil {
		d.Control = n.control
	}
	return d.DialContext(ctx, network, address)
}

func getLocalAddrFromNIC(nicName string) (*net.TCPAddr, error) {
	iface, err := net.InterfaceByName(nicName)
	if err != nil {
		return nil, fmt.Errorf("get interface by name: %w", err)
	}

	addrs, err := iface.Addrs()
	if err != nil {
		return nil, fmt.Errorf("get interface addresses: %w", err)
	}

	localAddr, ok := selectIPv4(addrs)
	if !ok {
		return nil, fmt.Errorf("no valid IPv4 address found for interface %s", nicName)
	}
	return localAddr, nil
}

func selectIPv4(addrs []net.Addr) (*net.TCPAddr, bool) {
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() {
			continue
		}
		// link-local (169.254.0.0/16) は経路を持たないため除外する。
		// ホスト側の nic-watcher も同じ除外をしている。
		if ipNet.IP.IsLinkLocalUnicast() {
			continue
		}
		if ipNet.IP.To4() == nil {
			continue
		}
		return &net.TCPAddr{IP: ipNet.IP}, true
	}

	return nil, false
}
