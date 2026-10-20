package nic

import (
	"context"
	"errors"
	"fmt"
	"net"
)

// DialContext は、指定したネットワークインターフェースから接続を確立します。
//
// Linux では SO_BINDTODEVICE でソケットをインターフェースに bind するため、ポリシールーティングに頼らずに送信元のインターフェースが決まります。
// インターフェースには宛先への経路が必要です。経路が無い場合は "no route to host" などのエラーになるか、インターフェースの種類によってはコンテキストの期限まで接続が完了しません。
// bind したソケットからは、ローカルの HTTP プロキシ（HTTPS_PROXY=http://127.0.0.1:...）などのループバックアドレスにも届きません。
// bind が許可されていない（EPERM）場合は、送信元アドレスの指定だけで接続します。
// Linux 以外の環境と、DisableBindToDevice を指定した場合は、送信元アドレスの指定だけで接続します。
type DialContext struct {
	nic string
	// bindToDevice is replaceable in tests. It is nil on non-Linux platforms and when binding is disabled.
	bindToDevice func(fd int, nic string) error
}

// DialContextConfig は、DialContext の設定です。
type DialContextConfig struct {
	// NIC は、送信元のネットワークインターフェース名です。
	NIC string

	// DisableBindToDevice を true にすると、ソケットをインターフェースに bind せず、送信元アドレスの指定だけで接続します。
	// Docker のブリッジネットワークのインターフェースなど、送信元のインターフェースをポリシールーティングで選ぶ場合に使います。
	DisableBindToDevice bool
}

// NewDialContext は、DialContext を生成します。
func NewDialContext(c DialContextConfig) (*DialContext, error) {
	if c.NIC == "" {
		return nil, errors.New("NIC is required")
	}
	// Do not check that the interface exists or resolve its address here.
	// Resolve it at dial time to allow an interface that appears after startup.
	dc := &DialContext{nic: c.NIC}
	if !c.DisableBindToDevice {
		dc.bindToDevice = bindToDevice
	}
	return dc, nil
}

// DialContext は、インターフェースから address へ接続します。
func (n *DialContext) DialContext(ctx context.Context, network string, address string) (net.Conn, error) {
	localAddr, err := getLocalAddrFromNIC(n.nic)
	if err != nil {
		return nil, fmt.Errorf("get local address for nic %s: %w", n.nic, err)
	}
	// Always set LocalAddr so that the source IP is set even where binding is unavailable.
	// Policy routing rules also match on the source IP.
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
		// Skip link-local addresses (169.254.0.0/16) because they have no route.
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
