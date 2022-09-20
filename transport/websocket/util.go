package websocket

import (
	"io"
	"net"
	"os"
	"syscall"

	"github.com/aptpod/iscp-go/errors"

	gwebsocket "github.com/gorilla/websocket"
	"nhooyr.io/websocket"
)

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

	if websocket.CloseStatus(err) == websocket.StatusNormalClosure {
		return true
	}
	if websocket.CloseStatus(err) == websocket.StatusGoingAway {
		return true
	}
	if gwebsocket.IsCloseError(
		err,
		gwebsocket.CloseNormalClosure,
		gwebsocket.CloseAbnormalClosure,
		gwebsocket.CloseGoingAway,
		gwebsocket.CloseNormalClosure,
		gwebsocket.CloseGoingAway,
		gwebsocket.CloseProtocolError,
		gwebsocket.CloseUnsupportedData,
		gwebsocket.CloseNoStatusReceived,
		gwebsocket.CloseAbnormalClosure,
		gwebsocket.CloseInvalidFramePayloadData,
		gwebsocket.ClosePolicyViolation,
		gwebsocket.CloseMessageTooBig,
		gwebsocket.CloseMandatoryExtension,
		gwebsocket.CloseInternalServerErr,
		gwebsocket.CloseServiceRestart,
		gwebsocket.CloseTryAgainLater,
		gwebsocket.CloseTLSHandshake,
	) {
		return true
	}

	if errors.Is(err, io.EOF) || errors.Is(err, gwebsocket.ErrCloseSent) {
		return true
	}
	return false
}
