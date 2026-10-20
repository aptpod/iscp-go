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
	// Without permission, e.g. before Linux 5.7, connect with only the source IP.
	if errors.Is(bindErr, unix.EPERM) {
		return nil
	}
	if bindErr != nil {
		return fmt.Errorf("bind to device %s: %w", n.nic, bindErr)
	}
	return nil
}
