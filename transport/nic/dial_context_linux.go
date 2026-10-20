package nic

import (
	"errors"
	"fmt"
	"syscall"

	"golang.org/x/sys/unix"
)

var bindToDevice = unix.BindToDevice

func (n *DialContext) control(_, _ string, c syscall.RawConn) error {
	var bindErr error
	if err := c.Control(func(fd uintptr) { bindErr = n.bindToDevice(int(fd), n.nic) }); err != nil {
		return err
	}
	// Linux 5.7 未満などで権限が無い場合は、送信元 IP の指定だけで接続する。
	if errors.Is(bindErr, unix.EPERM) {
		return nil
	}
	if bindErr != nil {
		return fmt.Errorf("bind to device %s: %w", n.nic, bindErr)
	}
	return nil
}
