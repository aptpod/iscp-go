package websocket

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aptpod/iscp-go/errors"
	"github.com/aptpod/iscp-go/log"
	"github.com/aptpod/iscp-go/transport"
)

// DialConfigは、Dialerの設定です。
type DialConfig struct {
	// URLは、接続先URLです。
	URL string
	// Tokenは、接続時に認証ヘッダーへ設定するトークンです。
	Token *Token
	// TLSConfigは、TLS設定です。
	TLSConfig *tls.Config

	// EnableMultipathTCPはMultipath TCPを有効化します。
	//
	// DEPRECATED: DialContextを使用してください。
	//     dialer := net.Dialer{}
	//     dialer.SetMultipathTCP(true)
	EnableMultipathTCP bool

	// DialContextはWebSocketトランスポートの内部で使用するDialContextを設定します。
	DialContext func(ctx context.Context, network, addr string) (net.Conn, error)
	// DialTLSContextはWebSocketトランスポートの内部で使用するDialTLSContextを設定します。
	DialTLSContext func(ctx context.Context, network, addr string) (net.Conn, error)

	// Proxyは、HTTPプロキシを設定します。
	//
	// http.Transport.Proxyを参照してください。
	Proxy func(*http.Request) (*url.URL, error)

	// DialTimeoutは、WebSocket接続のタイムアウトです。
	// 0に設定された場合、タイムアウトは設定されません。
	DialTimeout time.Duration
}

// DialFunc はConnを返却する関数です。 Tokenはオプショナルです。nilの可能性があります。
//
// 実装したDialFuncは、RegisterDialFuncを使用して登録します。
type DialFunc func(c DialConfig) (Conn, error)

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
	QueueSize:   32,
	DialTimeout: 10 * time.Second,
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

	// EnableMultipathTCPは、MultipathTCPを有効にします。
	EnableMultipathTCP bool

	// DialContextはWebSocketトランスポートの内部で使用するDialContextを設定します。
	DialContext func(ctx context.Context, network, addr string) (net.Conn, error)

	// DialTLSContextはWebSocketトランスポートの内部で使用するDialTLSContextを設定します。
	DialTLSContext func(ctx context.Context, network, addr string) (net.Conn, error)

	// Proxyは、HTTPプロキシを設定します。
	//
	// http.Transport.Proxyを参照してください。
	Proxy func(*http.Request) (*url.URL, error)

	// DialTimeoutは、WebSocket接続のタイムアウトです。
	// 0に設定された場合は、デフォルト値(10秒)が使用されます。
	DialTimeout time.Duration
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
	logger := log.NewStd()
	logger.Infof(context.Background(), "Dial: starting")

	// setup default
	if d.QueueSize == 0 {
		d.QueueSize = defaultDialerConfig.QueueSize
	}
	if d.DialTimeout == 0 {
		d.DialTimeout = defaultDialerConfig.DialTimeout
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
	logger.Infof(context.Background(), "Dial: URL generated", "url", wsURL.String())

	var tk *Token
	if d.TokenSource != nil {
		tk, err = d.TokenSource.Token()
		if err != nil {
			return nil, errors.Errorf("failed retrieving token: %w", err)
		}
		if tk.Header == "" {
			tk.Header = "Authorization"
		}
		logger.Infof(context.Background(), "Dial: token retrieved")
	} else {
		logger.Infof(context.Background(), "Dial: no token source")
	}

	logger.Infof(context.Background(), "Dial: calling dialFunc")
	wsconn, err := dialFunc(DialConfig{
		URL:                wsURL.String(),
		Token:              tk,
		TLSConfig:          d.TLSConfig,
		EnableMultipathTCP: d.EnableMultipathTCP,
		DialContext:        d.DialContext,
		DialTLSContext:     d.DialTLSContext,
		Proxy:              d.Proxy,
		DialTimeout:        d.DialTimeout,
	})
	if err != nil {
		return nil, err
	}
	logger.Infof(context.Background(), "Dial: dialFunc completed")

	logger.Infof(context.Background(), "Dial: creating transport")
	return New(Config{
		Conn:              wsconn,
		CompressConfig:    cc.CompressConfig,
		NegotiationParams: params,
		// Advanced settings
		QueueSize: d.QueueSize,
	}), nil
}
