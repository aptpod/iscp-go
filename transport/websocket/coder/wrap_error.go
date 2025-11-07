package coder

import (
	"context"
	"fmt"
	"net"
	"os"

	cwebsocket "github.com/coder/websocket"

	"github.com/aptpod/iscp-go/errors"
	"github.com/aptpod/iscp-go/transport"
)

func handleError(err error) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, net.ErrClosed) || errors.Is(err, context.Canceled) {
		return fmt.Errorf("%+v: %w", err, errors.ErrConnectionClosed)
	}

	var netErr *net.OpError
	if errors.As(err, &netErr) {
		return fmt.Errorf("%+v: %w", err, errors.ErrConnectionClosed)
	}
	var sysCallErr *os.SyscallError
	if errors.As(err, &sysCallErr) {
		return fmt.Errorf("%+v: %w", err, errors.ErrConnectionClosed)
	}

	var status transport.CloseStatus
	switch cwebsocket.CloseStatus(err) {
	case cwebsocket.StatusNormalClosure:
		status = transport.CloseStatusNormal
	case cwebsocket.StatusGoingAway:
		status = transport.CloseStatusGoingAway
	case cwebsocket.StatusAbnormalClosure:
		status = transport.CloseStatusAbnormal
	case cwebsocket.StatusInternalError:
		status = transport.CloseStatusInternalError
	default:
		status = transport.CloseStatusInternalError
	}
	return fmt.Errorf("%+v: %w", err, transport.GetCloseStatusError(status))
}
