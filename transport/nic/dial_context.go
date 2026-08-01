package nic

import (
	"context"
	"errors"
	"fmt"
	"net"
)

type DialContext struct {
	nic string
}

type DialContextConfig struct {
	NIC string
}

func NewDialContext(c DialContextConfig) (*DialContext, error) {
	if c.NIC == "" {
		return nil, errors.New("NIC is required")
	}
	// インターフェースの存在確認とアドレス解決はここでは行わない。
	// 起動時に存在しない NIC が後から現れる場合を許すため、解決は dial 時に行う。
	return &DialContext{nic: c.NIC}, nil
}

func (n *DialContext) DialContext(ctx context.Context, network string, address string) (net.Conn, error) {
	localAddr, err := getLocalAddrFromNIC(n.nic)
	if err != nil {
		return nil, fmt.Errorf("get local address for nic %s: %w", n.nic, err)
	}
	d := &net.Dialer{LocalAddr: localAddr}
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
