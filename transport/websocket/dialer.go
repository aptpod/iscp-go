package websocket

import (
	"crypto/tls"
	"fmt"
	"net/url"
	"strings"

	"github.com/aptpod/iscp-go/errors"
	"github.com/aptpod/iscp-go/transport"
)

// DialFunc はConnを返却する関数です。 Tokenはオプショナルです。nilの可能性があります。
//
// 実装したDialFuncは、RegisterDialFuncを使用して登録します。
type DialFunc func(string, *Token, *tls.Config) (Conn, error)

var dialFunc DialFunc

// RegisterDialFuncは、DialFuncを登録します。
//
// DialFuncを登録すると、WebSocketトランスポートはライブラリ内で登録されたDialFuncを使用します。
func RegisterDialFunc(f DialFunc) {
	if dialFunc != nil {
		panic("already registered dialFunc")
	}
	dialFunc = f
}

var defaultDialerConfig = DialerConfig{
	QueueSize: 32,
}

// DialerConfigはDialerの設定です。
type DialerConfig struct {
	// QueueSize は、トランスポートとメッセージをやり取りする際のメッセージキューの長さです。
	// 0 に設定された場合は、 DefaultQueueSize の値が使用されます。
	QueueSize int

	// Pathはパスを指定します
	Path string

	// EnableTLSは TLSアクセスするかどうかを設定します。
	EnableTLS bool

	// TokenSourceは、接続時に認証ヘッダーへ設定するトークンを取得します。
	// Dialerは取得されたトークンを認証ヘッダーとして利用します。
	TokenSource TokenSource

	// TLSConfigは、TLS設定です。
	TLSConfig *tls.Config
}

// Tokenはトークンを表します。
type Token struct {
	// Tokenはトークン文字列です。
	Token string

	// Headerはヘッダ名を指定します。デフォルトは `Authorization` です。
	Header string
}

// StaticTokenSourceは、静的に設定されたトークンを常に返却するTokenSource実装です。
type StaticTokenSource struct {
	StaticToken *Token
}

// TokenはTokenを返却します。
func (ts *StaticTokenSource) Token() (*Token, error) {
	return ts.StaticToken, nil
}

// TokenSourceは、認証トークンの取得用インターフェースです。
//
// ライブラリはこのインターフェースをWebSocket認証時に呼び出します。
type TokenSource interface {
	Token() (*Token, error)
}

// Dialは、デフォルト設定を使ってトランスポート接続を開始します。
func Dial(c transport.DialConfig) (transport.Transport, error) {
	return DialWithConfig(c, defaultDialerConfig)
}

// DialWithConfigは、トランスポート接続を開始します。
func DialWithConfig(c transport.DialConfig, cc DialerConfig) (transport.Transport, error) {
	d := &Dialer{
		DialerConfig: cc,
	}
	return d.Dial(c)
}

// Dialerは、トランスポート接続を開始します。
type Dialer struct {
	DialerConfig
}

// NewDefaultDialerは、デフォルト設定のDialerを返却します。
func NewDefaultDialer() *Dialer {
	return NewDialer(defaultDialerConfig)
}

// NewDialerは、Dialerを返却します。
func NewDialer(c DialerConfig) *Dialer {
	return &Dialer{DialerConfig: c}
}

// Dialは、トランスポート接続を開始します。
func (d *Dialer) Dial(cc transport.DialConfig) (transport.Transport, error) {
	// setup default
	if d.QueueSize == 0 {
		d.QueueSize = defaultDialerConfig.QueueSize
	}

	var schema string
	if d.EnableTLS {
		schema = "wss"
	} else {
		schema = "ws"
	}

	params := NegotiationParams{cc.NegotiationParams()}
	values, err := params.MarshalURLValues()
	if err != nil {
		return nil, errors.Errorf("MarshalURLValues failed for negotiation: %w", err)
	}

	u := fmt.Sprintf("%s://%s%s?%s", schema, cc.Address, strings.TrimSuffix(d.Path, "/"), values.Encode())
	wsURL, err := url.Parse(u)
	if err != nil {
		return nil, errors.Errorf("invalid url: %w", err)
	}

	var tk *Token
	if d.TokenSource != nil {
		tk, err = d.TokenSource.Token()
		if err != nil {
			return nil, errors.Errorf("failed retrieving token: %w", err)
		}
		if tk.Header == "" {
			tk.Header = "Authorization"
		}
	}

	wsconn, err := dialFunc(wsURL.String(), tk, d.TLSConfig)
	if err != nil {
		return nil, err
	}

	return New(Config{
		Conn:              wsconn,
		CompressConfig:    cc.CompressConfig,
		NegotiationParams: params,
		// Advanced settings
		QueueSize: d.QueueSize,
	}), nil
}
