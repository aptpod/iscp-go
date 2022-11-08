package errors

import (
	"errors"
	"fmt"

	"github.com/aptpod/iscp-go/message"
)

var (
	// ErrISCPはiscpライブラリで定義されている基底エラーです。
	ErrISCP = errors.New("iscp")
	// ErrConnectionClosedは、トランスポートが閉じられている状態でトランスポートへの読み書きをした場合のエラーです。
	ErrConnectionClosed = fmt.Errorf("closed iscp connection: %w", ErrISCP)
	// ErrStreamClosedは、ストリームが閉じられている状態でトランスポートへの読み書きをした場合のエラーです。
	ErrStreamClosed = fmt.Errorf("closed iscp stream: %w", ErrISCP)
	// ErrMalformedMessage、メッセージのエンコードやデコードに失敗した時のエラーです。
	ErrMalformedMessage = fmt.Errorf("malformed message: %w", ErrISCP)
	// ErrMessageTooLargeは、メッセージが大きすぎる場合のエラーです。
	ErrMessageTooLarge = fmt.Errorf("message is too large: %w", ErrMalformedMessage)
)

// iSCPでの通信中に、失敗を意味する結果コードが含まれたメッセージを受信した場合に送出される例外です。
type FailedMessageError struct {
	ResultCode      message.ResultCode // 結果コード
	ResultString    string             // 結果文字列
	ReceivedMessage message.Message    // 受信メッセージ
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
