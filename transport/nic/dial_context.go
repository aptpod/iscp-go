package nic

import (
	"context"
	"fmt"
	"net"
)

type DialContext struct {
	nicName string
}

type DialContextConfig struct {
	NIC string
}

func NewDialContext(c DialContextConfig) (*DialContext, error) {
	return &DialContext{nicName: c.NIC}, nil
}

func (n *DialContext) DialContext(ctx context.Context, network string, address string) (net.Conn, error) {
	localAddr, err := getLocalAddrFromNIC(n.nicName)
	if err != nil {
		return nil, fmt.Errorf("get local address for nic %s: %w", n.nicName, err)
	}
	dialer := &net.Dialer{LocalAddr: localAddr}
	return dialer.DialContext(ctx, network, address)
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

	return selectIPv4(addrs)
}

// selectIPv4 は、インターフェースのアドレス一覧から bind に使う IPv4 アドレスを1つ選ぶ。
//
// loopback は常に除外する。link-local (169.254.0.0/16) は DHCP 失敗時に付く経路の無い
// アドレスであることが多いため優先度を下げるが、除外はしない。IPv4LL の直結リンク
// （DHCP サーバー不在の Ethernet 直結など）では link-local しか持たない NIC が正常な
// bind 先になるため、他に候補が無ければそれを選ぶ。
func selectIPv4(addrs []net.Addr) (*net.TCPAddr, error) {
	var linkLocal *net.TCPAddr
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() {
			continue
		}
		ip4 := ipNet.IP.To4()
		if ip4 == nil {
			continue
		}
		if ipNet.IP.IsLinkLocalUnicast() {
			if linkLocal == nil {
				linkLocal = &net.TCPAddr{IP: ip4}
			}
			continue
		}
		return &net.TCPAddr{IP: ip4}, nil
	}
	if linkLocal != nil {
		return linkLocal, nil
	}

	return nil, fmt.Errorf("no valid IPv4 address found")
}
