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

	"github.com/aptpod/iscp-go/v2/errors"
	"github.com/aptpod/iscp-go/v2/log"
	"github.com/aptpod/iscp-go/v2/transport"
	"github.com/aptpod/iscp-go/v2/transport/compress"
)

// DialConfigは、Dialerの設定です。
type DialConfig struct {
	// Contextは、接続確立を中断するためのcontextです。
	//
	// この構造体は呼び出しごとに構築されるパラメータオブジェクトであるため、
	// contextをフィールドとして持ちます。nilの場合はcontext.Background()と
	// して扱われます。DialFuncの実装はこのcontextを尊重することが推奨され
	// ますが、無視しても互換性は壊れません（その場合はDialTimeoutなど実装
	// 固有のタイムアウトだけが上限になります）。
	Context context.Context

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

// DialFunc はWebSocket接続を確立してConnを返却する関数です。
// Tokenはオプショナルで、nilの可能性があります。
//
// デフォルトでは coder/websocket が使用されます。
// gorilla/websocket を使用する場合は、DialerConfig.DialFunc に GorillaDial を指定してください。
type DialFunc func(c DialConfig) (Conn, error)

// dialFunc はパッケージデフォルトのDialFuncです。
// dial_coder.go の init() で coderDial が設定されます。
var dialFunc DialFunc

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

	// DialFunc は WebSocket 接続を確立する関数です。
	// nilの場合、パッケージデフォルト（coder/websocket）が使用されます。
	// gorilla/websocket を使用する場合は GorillaDial を指定してください。
	DialFunc DialFunc
}

// Token は transport.Token の型エイリアスです。
type Token = transport.Token

// TokenSource は transport.TokenSource の型エイリアスです。
type TokenSource = transport.TokenSource

// StaticTokenSource は transport.StaticTokenSource の型エイリアスです。
type StaticTokenSource = transport.StaticTokenSource

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

	// lastCapturedConn は、最後にキャプチャしたTCP接続を保持します。
	// buildHTTPTransportでDialContextをラップし、接続時にここに保存されます。
	lastCapturedConn   net.Conn
	lastCapturedConnMu sync.RWMutex
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

	// d.DialContext と書くと Dialer の DialContext メソッド（transport.ContextDialer）
	// に解決されるため、埋め込みフィールドは明示パスで参照する。
	if d.DialerConfig.DialContext != nil {
		tr.DialContext = d.DialerConfig.DialContext
	}
	if d.DialTLSContext != nil {
		tr.DialTLSContext = d.DialTLSContext
	}

	if d.Proxy != nil {
		tr.Proxy = d.Proxy
	}

	// DialContextをラップしてTCP接続をキャプチャする
	// これにより、メトリクス取得のためにunderlying TCP接続にアクセス可能になる
	baseDialContext := tr.DialContext
	if baseDialContext == nil {
		baseDialContext = (&net.Dialer{}).DialContext
	}
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := baseDialContext(ctx, network, addr)
		if err == nil {
			d.lastCapturedConnMu.Lock()
			d.lastCapturedConn = conn
			d.lastCapturedConnMu.Unlock()
		}
		return conn, err
	}

	return tr
}

// GetLastCapturedConn は、最後にキャプチャしたTCP接続を返却します。
// メトリクス取得などに使用します。
func (d *Dialer) GetLastCapturedConn() net.Conn {
	d.lastCapturedConnMu.RLock()
	defer d.lastCapturedConnMu.RUnlock()
	return d.lastCapturedConn
}

// Dialは、トランスポート接続を開始します。
func (d *Dialer) Dial(cc transport.DialConfig) (transport.Transport, error) {
	return d.DialContext(context.Background(), cc)
}

// DialContextは、ctxを尊重してトランスポート接続を開始します。
// transport.ContextDialerの実装です。
//
// ctxのキャンセル・期限切れで進行中の接続確立が中断されます。DialTimeoutが
// 設定されている場合は、ctxとDialTimeoutの早い方が上限になります。
func (d *Dialer) DialContext(ctx context.Context, cc transport.DialConfig) (transport.Transport, error) {
	// デフォルト値をローカル変数で適用（レシーバを変更しない）
	queueSize := d.QueueSize
	if queueSize == 0 {
		queueSize = defaultDialerConfig.QueueSize
	}
	dialTimeout := d.DialTimeout
	if dialTimeout == 0 {
		dialTimeout = defaultDialerConfig.DialTimeout
	}
	logger := d.Logger
	if logger == nil {
		logger = log.NewNop()
	}

	var schema string
	if d.EnableTLS {
		schema = "wss"
	} else {
		schema = "ws"
	}

	params := cc.NegotiationParams()
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

	logger.Infof(ctx, "Dial: starting connection (url=%s, hasToken=%v, timeout=%v)", wsURL.String(), hasToken, dialTimeout)

	// DialFunc の解決: DialerConfig 指定 > グローバルデフォルト > nilガード
	df := d.DialFunc
	if df == nil {
		df = dialFunc
	}
	if df == nil {
		return nil, errors.Errorf("websocket dial function not registered")
	}

	wsconn, err := df(DialConfig{
		Context:            ctx,
		URL:                wsURL.String(),
		Token:              tk,
		TLSConfig:          d.TLSConfig,
		EnableMultipathTCP: d.EnableMultipathTCP,
		DialContext:        d.DialerConfig.DialContext,
		DialTLSContext:     d.DialTLSContext,
		Proxy:              d.Proxy,
		DialTimeout:        dialTimeout,
		HTTPTransport:      d.getHTTPTransport(),
	})
	if err != nil {
		return nil, err
	}

	// HTTPTransport経由で接続した場合、Dialer側でキャプチャしたTCP接続をConnに設定する
	// これにより、coder/nhooyrなどのHTTPTransportを使用する実装でもTCP_INFOを取得可能になる
	if capturedConn := d.GetLastCapturedConn(); capturedConn != nil {
		wsconn.SetUnderlyingConn(capturedConn)
	}

	logger.Infof(ctx, "Dial: connection established successfully")

	// v4: V4Transport が圧縮を担当するため base の圧縮を無効化
	if cc.TransportType != "" {
		tr := New(Config{
			Conn:              wsconn,
			CompressConfig:    compress.Config{},
			NegotiationParams: params.WithoutCompression(),
			UseMessageFraming: cc.TransportType == transport.NegotiationNameWebSocket,
			QueueSize:         queueSize,
		})
		return transport.NewV4Transport(tr, params, cc.CompressConfig), nil
	}
	return New(Config{
		Conn:              wsconn,
		CompressConfig:    cc.CompressConfig,
		NegotiationParams: params,
		UseMessageFraming: cc.TransportType == transport.NegotiationNameWebSocket,
		QueueSize:         queueSize,
	}), nil
}
