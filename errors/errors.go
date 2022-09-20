package errors

import (
	"errors"
	"fmt"

	"github.com/aptpod/iscp-go/message"
)

var (
	ErrISCP             = errors.New("iscp")
	ErrConnectionClosed = fmt.Errorf("closed iscp connection: %w", ErrISCP)
	ErrStreamClosed     = fmt.Errorf("closed iscp stream: %w", ErrISCP)
	ErrMalformedMessage = fmt.Errorf("malformed message: %w", ErrISCP)
	// ErrMessageTooLargeは、メッセージが大きすぎる場合に返されます。
	ErrMessageTooLarge = fmt.Errorf("message is too large: %w", ErrMalformedMessage)
)

type FailedMessageError struct {
	ResultCode   message.ResultCode
	ResultString string
	Message      message.Message
}

func (e FailedMessageError) Error() string {
	return fmt.Sprintf("result_code: %v result_message: %v", e.ResultCode, e.ResultString)
}

func (e FailedMessageError) Is(err error) bool {
	return err == ErrISCP
}

func AsFailedMessageError(err error) (*FailedMessageError, bool) {
	var res FailedMessageError
	ok := As(err, &res)
	return &res, ok
}

func New(text string) error {
	return errors.New(text)
}

func Errorf(format string, a ...any) error {
	return fmt.Errorf(format, a...)
}

func Is(err, target error) bool {
	return errors.Is(err, target)
}

func As(err error, target any) bool {
	return errors.As(err, target)
}
