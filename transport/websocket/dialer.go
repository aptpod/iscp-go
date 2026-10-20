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

// dialFunc is the package default DialFunc.
// coderDial is set by init() in dial_coder.go.
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
	//
	// TokenSourceWithContext を実装している場合、dial の ctx を渡した
	// TokenWithContext が呼ばれます。実装していない TokenSource では、
	// dial に渡した ctx のキャンセルは Token() の完了までは効きません。
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

	// ReadTimeoutは、読み込み操作のタイムアウト時間です。
	// 0に設定された場合は、 DefaultReadTimeout の値が使用されます。
	ReadTimeout time.Duration

	// WriteTimeoutは、書き込み操作のタイムアウト時間です。
	// 0に設定された場合は、 DefaultWriteTimeout の値が使用されます。
	WriteTimeout time.Duration

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

// TokenSourceWithContext は transport.TokenSourceWithContext の型エイリアスです。
type TokenSourceWithContext = transport.TokenSourceWithContext

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

	// lastCapturedConn holds the TCP connection captured last.
	// buildHTTPTransport wraps DialContext, and the connection is saved here when connecting.
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

// getHTTPTransport returns the http.Transport for the Dialer.
// On the first call it creates an http.Transport based on the DialerConfig settings, and reuses it afterwards.
func (d *Dialer) getHTTPTransport() *http.Transport {
	d.httpTransportOnce.Do(func() {
		d.httpTransport = d.buildHTTPTransport()
	})
	return d.httpTransport
}

// buildHTTPTransport creates an http.Transport based on the DialerConfig settings.
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

	// Writing d.DialContext would resolve to the DialContext method of Dialer (transport.ContextDialer),
	// so the embedded field is referenced with an explicit path.
	if d.DialerConfig.DialContext != nil {
		tr.DialContext = d.DialerConfig.DialContext
	}
	if d.DialTLSContext != nil {
		tr.DialTLSContext = d.DialTLSContext
	}

	if d.Proxy != nil {
		tr.Proxy = d.Proxy
	}

	// Wrap DialContext to capture the TCP connection.
	// This makes the underlying TCP connection accessible for obtaining metrics.
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
	// Apply the defaults to local variables (do not modify the receiver)
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
		// If TokenSourceWithContext is implemented, pass ctx. A TokenSource that does not
		// implement it calls Token() as before, so cancellation of the dial's ctx
		// does not take effect until Token() completes. We do not wrap it in a goroutine to make it
		// look interruptible (that would leave the retrieval running after the caller gave up,
		// leaking it).
		if ts, ok := d.TokenSource.(TokenSourceWithContext); ok {
			tk, err = ts.TokenWithContext(ctx)
		} else {
			tk, err = d.TokenSource.Token()
		}
		if err != nil {
			return nil, errors.Errorf("failed retrieving token: %w", err)
		}
		if tk.Header == "" {
			tk.Header = "Authorization"
		}
		hasToken = true
	}

	logger.Infof(ctx, "Dial: starting connection (url=%s, hasToken=%v, timeout=%v)", wsURL.String(), hasToken, dialTimeout)

	// Resolution of DialFunc: DialerConfig setting > global default > nil guard
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

	// When connected via HTTPTransport, set the TCP connection captured on the Dialer side to the Conn.
	// This makes it possible to obtain TCP_INFO even for implementations that use HTTPTransport, such as coder/nhooyr.
	if capturedConn := d.GetLastCapturedConn(); capturedConn != nil {
		wsconn.SetUnderlyingConn(capturedConn)
	}

	logger.Infof(ctx, "Dial: connection established successfully")

	// v4: V4Transport handles compression, so disable the compression of the base
	if cc.TransportType != "" {
		tr := New(Config{
			Conn:              wsconn,
			CompressConfig:    compress.Config{},
			NegotiationParams: params.WithoutCompression(),
			UseMessageFraming: cc.TransportType == transport.NegotiationNameWebSocket,
			QueueSize:         queueSize,
			ReadTimeout:       d.ReadTimeout,
			WriteTimeout:      d.WriteTimeout,
		})
		return transport.NewV4Transport(tr, params, cc.CompressConfig), nil
	}
	return New(Config{
		Conn:              wsconn,
		CompressConfig:    cc.CompressConfig,
		NegotiationParams: params,
		UseMessageFraming: cc.TransportType == transport.NegotiationNameWebSocket,
		QueueSize:         queueSize,
		ReadTimeout:       d.ReadTimeout,
		WriteTimeout:      d.WriteTimeout,
	}), nil
}
