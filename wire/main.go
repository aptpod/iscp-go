/*
Package wire は、 iSCP のワイヤレベルのプロトコルを定義するパッケージです。
このパッケージは、 iSCP が使用するメッセージ構造およびメッセージの送受信シーケンスを定義します。
また、 iSCP ブローカーとクライアントが利用する機能を抽象化したインターフェースについても定義します。
*/
package wire

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

/*
Server は以下のエラーを返します。
*/
var (
	// ErrServerStarted はすでにサーバーがスタートしているときに返されます。
	ErrServerStarted = errors.New("server already started")

	// ErrServerClosed はすでにサーバーがクローズしているときに返されます。
	ErrServerClosed = errors.New("server closed")

	// ErrListenerClosed は、Listenerが終了しているときに返されます。
	ErrListenerClosed = errors.New("listener already closed")
)
