package nic

import (
	"context"
	"fmt"
	"net"
)

type DialContext struct {
	dialer *net.Dialer
}

type DialContextConfig struct {
	NIC string
}

func NewDialContext(c DialContextConfig) (*DialContext, error) {
	localAddr, err := getLocalAddrFromNIC(c.NIC)
	if err != nil {
		return nil, fmt.Errorf("get local address: %w", err)
	}
	return &DialContext{dialer: &net.Dialer{
		LocalAddr: localAddr,
	}}, nil
}

func (n *DialContext) DialContext(ctx context.Context, network string, address string) (net.Conn, error) {
	return n.dialer.DialContext(ctx, network, address)
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

	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
			if ipNet.IP.To4() != nil {
				return &net.TCPAddr{
					IP: ipNet.IP,
				}, nil
			}
		}
	}

	return nil, fmt.Errorf("no valid IPv4 address found for interface %s", nicName)
}
