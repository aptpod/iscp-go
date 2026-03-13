package gorilla

import (
	"fmt"
	"net"
	"os"
	"syscall"

	"github.com/aptpod/iscp-go/errors"
	"github.com/aptpod/iscp-go/transport"

	gwebsocket "github.com/gorilla/websocket"
)

func handlerError(err error) error {
	if err == nil {
		return nil
	}
	if isErrTransportClosed(err) {
		return fmt.Errorf("failed to write control message %+v: %w", err, transport.ErrAlreadyClosed)
	}
	var closeErr *gwebsocket.CloseError
	if !errors.As(err, &closeErr) {
		return fmt.Errorf("failed to write control message: %w", err)
	}

	var status transport.CloseStatus
	switch closeErr.Code {
	case gwebsocket.CloseNormalClosure:
		status = transport.CloseStatusNormal
	case gwebsocket.CloseGoingAway:
		status = transport.CloseStatusGoingAway
	case gwebsocket.CloseAbnormalClosure:
		status = transport.CloseStatusAbnormal
	case gwebsocket.CloseInternalServerErr:
		status = transport.CloseStatusInternalError
	default:
		status = transport.CloseStatusInternalError
	}
	return fmt.Errorf("failed to write control message cause[%+v]: %w", err, transport.GetCloseStatusError(status))
}

func isErrTransportClosed(err error) bool {
	if err, ok := err.(*net.OpError); ok {
		if errors.Is(err, syscall.EPIPE) {
			return true
		}
		if err, ok := err.Unwrap().(*os.SyscallError); ok {
			return err.Unwrap().Error() == "connection reset by peer"
		}
		return err.Unwrap().Error() == "use of closed network connection"
	}
	return false
}
