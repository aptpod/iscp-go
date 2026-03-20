package iscp

import "github.com/aptpod/iscp-go/errors"

/*
Conn は以下のエラーを返します。
*/
var (
	// ErrConnTimeout は、トランスポートからの読み書きを所定の時間待機しても応答が無い場合に返されます。
	ErrConnTimeout = errors.New("connection timeout")

	// ErrUnsupportedTransport は、サポートしていないトランスポートを指定したときに返されます。
	ErrUnsupportedTransport = errors.New("unsupported transport")

	// ErrUnauthorized は、認証されていないときに返されます。
	ErrUnauthorized = errors.Errorf("unauthorized : %w", ErrInvalidConnectRequest)

	// ErrInvalidConnectRequest は、ConnectRequestが不正の場合に返されます。
	ErrInvalidConnectRequest = errors.New("invalid connect request")

	// ErrMessageTooLargeは、メッセージが大きすぎる場合に返されます。
	ErrMessageTooLarge = errors.New("message is too large")
)
