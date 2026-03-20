package iscp

import "github.com/aptpod/iscp-go/errors"

/*
Conn は以下のエラーを返します。
*/
var (
	// ErrUnauthorized は、認証されていないときに返されます。
	ErrUnauthorized = errors.Errorf("unauthorized : %w", ErrInvalidConnectRequest)

	// ErrInvalidConnectRequest は、ConnectRequestが不正の場合に返されます。
	ErrInvalidConnectRequest = errors.New("invalid connect request")
)
