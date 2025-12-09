package websocket

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
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

	// HTTPTransportは、WebSocket接続に使用するhttp.Transportです。
	// 設定された場合、TLSConfig, EnableMultipathTCP, DialContext, DialTLSContext, Proxyの設定は無視されます。
	// nilの場合は、上記の設定を元に新しいhttp.Transportが作成されます。
	HTTPTransport *http.Transport
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

	// Loggerは、ログ出力に使用するロガーです。
	// nilに設定された場合は、log.NewNop()が使用されます。
	Logger log.Logger
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

	httpTransport     *http.Transport
	httpTransportOnce sync.Once
}

// NewDefaultDialerは、デフォルト設定のDialerを返却します。
func NewDefaultDialer() *Dialer {
	return NewDialer(defaultDialerConfig)
}

// NewDialerは、Dialerを返却します。
func NewDialer(c DialerConfig) *Dialer {
	return &Dialer{DialerConfig: c}
}

// getHTTPTransport は、Dialer用のhttp.Transportを返却します。
// 初回呼び出し時にDialerConfigの設定を元にhttp.Transportを作成し、以降は再利用します。
func (d *Dialer) getHTTPTransport() *http.Transport {
	d.httpTransportOnce.Do(func() {
		d.httpTransport = d.buildHTTPTransport()
	})
	return d.httpTransport
}

// buildHTTPTransport は、DialerConfigの設定を元にhttp.Transportを作成します。
func (d *Dialer) buildHTTPTransport() *http.Transport {
	tr := http.DefaultTransport.(*http.Transport).Clone()

	if d.TLSConfig != nil {
		tr.TLSClientConfig = d.TLSConfig
	}

	if d.EnableMultipathTCP {
		dialer := net.Dialer{}
		dialer.SetMultipathTCP(d.EnableMultipathTCP)
		tr.DialContext = dialer.DialContext
	}

	if d.DialContext != nil {
		tr.DialContext = d.DialContext
	}
	if d.DialTLSContext != nil {
		tr.DialTLSContext = d.DialTLSContext
	}

	if d.Proxy != nil {
		tr.Proxy = d.Proxy
	}

	return tr
}

// Dialは、トランスポート接続を開始します。
func (d *Dialer) Dial(cc transport.DialConfig) (transport.Transport, error) {
	// setup default
	if d.QueueSize == 0 {
		d.QueueSize = defaultDialerConfig.QueueSize
	}
	if d.DialTimeout == 0 {
		d.DialTimeout = defaultDialerConfig.DialTimeout
	}
	if d.Logger == nil {
		d.Logger = log.NewNop()
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
	hasToken := false
	if d.TokenSource != nil {
		tk, err = d.TokenSource.Token()
		if err != nil {
			return nil, errors.Errorf("failed retrieving token: %w", err)
		}
		if tk.Header == "" {
			tk.Header = "Authorization"
		}
		hasToken = true
	}

	d.Logger.Infof(context.Background(), "Dial: starting connection (url=%s, hasToken=%v, timeout=%v)", wsURL.String(), hasToken, d.DialTimeout)

	wsconn, err := dialFunc(DialConfig{
		URL:                wsURL.String(),
		Token:              tk,
		TLSConfig:          d.TLSConfig,
		EnableMultipathTCP: d.EnableMultipathTCP,
		DialContext:        d.DialContext,
		DialTLSContext:     d.DialTLSContext,
		Proxy:              d.Proxy,
		DialTimeout:        d.DialTimeout,
		HTTPTransport:      d.getHTTPTransport(),
	})
	if err != nil {
		return nil, err
	}

	d.Logger.Infof(context.Background(), "Dial: connection established successfully")

	return New(Config{
		Conn:              wsconn,
		CompressConfig:    cc.CompressConfig,
		NegotiationParams: params,
		// Advanced settings
		QueueSize: d.QueueSize,
	}), nil
}
