//go:build !linux

package nic

import "syscall"

var bindToDevice func(fd int, nic string) error

func (n *DialContext) control(_, _ string, _ syscall.RawConn) error {
	return nil
}
