package webtransport

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	quicgo "github.com/quic-go/quic-go"
	webtransgo "github.com/quic-go/webtransport-go"

	"github.com/aptpod/iscp-go/v2/errors"
	"github.com/aptpod/iscp-go/v2/transport"
	"github.com/aptpod/iscp-go/v2/transport/compress"
)

const (
	defaultQueueSize = 32
)

var defaultDialerConfig = DialerConfig{
	QueueSize: defaultQueueSize,
	TLSConfig: &tls.Config{},
}

// DialerConfigは、Dialerの設定です。
type DialerConfig struct {
	// QueueSize は、トランスポートとメッセージをやり取りする際のメッセージキューの長さです。
	// 0 に設定された場合は、 DefaultQueueSize の値が使用されます。
	QueueSize int

	// Pathはパスを指定します
	Path string

	// TLSConfigは TLSの設定です。
	TLSConfig *tls.Config

	// TokenSourceは、接続時に認証ヘッダーへ設定するトークンを取得します。
	// Dialerは取得されたトークンを認証ヘッダーとして利用します。
	//
	// TokenSourceWithContext を実装している場合、dial の ctx を渡した
	// TokenWithContext が呼ばれます。実装していない TokenSource では、
	// dial に渡した ctx のキャンセルは Token() の完了までは効きません。
	TokenSource TokenSource

	// MaxIdleTimeoutは、無通信のままコネクションを維持する最大時間です。
	// この時間を超えるとコネクションは切断され、ブロックしていた書き込みも解除されます。
	// 0 に設定された場合は、quic-goの既定値(30秒)が使用されます。
	MaxIdleTimeout time.Duration

	// KeepAlivePeriodは、keep-alive PINGの送信間隔です。
	// 0 に設定された場合は、keep-aliveは無効です。
	KeepAlivePeriod time.Duration
}

// quicConfigは、DialerConfigからquic-goの設定を組み立てます。
func (c DialerConfig) quicConfig() *quicgo.Config {
	return &quicgo.Config{
		EnableDatagrams:                  true,
		EnableStreamResetPartialDelivery: true,
		MaxIdleTimeout:                   c.MaxIdleTimeout,
		KeepAlivePeriod:                  c.KeepAlivePeriod,
	}
}

// Dialerは、WebTransportのトランスポートを接続します。
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

// Token は transport.Token の型エイリアスです。
type Token = transport.Token

// TokenSource は transport.TokenSource の型エイリアスです。
type TokenSource = transport.TokenSource

// TokenSourceWithContext は transport.TokenSourceWithContext の型エイリアスです。
type TokenSourceWithContext = transport.TokenSourceWithContext

// StaticTokenSource は transport.StaticTokenSource の型エイリアスです。
type StaticTokenSource = transport.StaticTokenSource

// Dialは、トランスポートを接続します。
func (d *Dialer) Dial(c transport.DialConfig) (transport.Transport, error) {
	return d.DialContext(context.Background(), c)
}

// DialContextは、ctxを尊重してトランスポートを接続します。
// transport.ContextDialerの実装です。
func (d *Dialer) DialContext(ctx context.Context, c transport.DialConfig) (transport.Transport, error) {
	if d.TLSConfig == nil {
		d.TLSConfig = defaultDialerConfig.TLSConfig
	}
	dialer := &webtransgo.Dialer{
		TLSClientConfig: d.TLSConfig,
		QUICConfig:      d.quicConfig(),
	}

	params := c.NegotiationParams()
	values, err := params.MarshalURLValues()
	if err != nil {
		return nil, errors.Errorf("MarshalURLValues failed for negotiation: %w", err)
	}
	webtransURL, err := url.Parse(fmt.Sprintf("https://%s/%s?%s", c.Address, strings.TrimPrefix(strings.TrimSuffix(d.Path, "/"), "/"), values.Encode()))
	if err != nil {
		return nil, errors.Errorf("invalid url: %w", err)
	}

	var tk *Token
	if d.TokenSource != nil {
		// TokenSourceWithContext を実装していれば ctx を渡す。未実装の
		// TokenSource は従来どおり Token() を呼ぶため、dial の ctx の
		// キャンセルは Token() の完了までは効かない。goroutine で包んで
		// 中断可能に見せることはしない（呼び出し元が諦めた後も取得処理が
		// 残留するリーク構造になるため）。
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
	}
	var header http.Header
	if tk != nil {
		header = http.Header{}
		header.Add(tk.Header, tk.Token)
	}

	//nolint
	_, conn, err := dialer.Dial(ctx, webtransURL.String(), header)
	if err != nil {
		return nil, errors.Errorf("webtransport dialing failed on [%s]: %w", webtransURL.String(), err)
	}

	// v4: V4Transport が圧縮を担当するため base の圧縮を無効化
	if c.TransportType != "" {
		ts, err := New(Config{
			Connection:        conn,
			QueueSize:         d.QueueSize,
			CompressConfig:    compress.Config{},
			NegotiationParams: params.WithoutCompression(),
		})
		if err != nil {
			defer conn.CloseWithError(webtransgo.SessionErrorCode(0), "")
			return nil, err
		}
		return transport.NewV4Transport(ts, params, c.CompressConfig), nil
	}
	ts, err := New(Config{
		Connection:        conn,
		QueueSize:         d.QueueSize,
		CompressConfig:    c.CompressConfig,
		NegotiationParams: params,
	})
	if err != nil {
		defer conn.CloseWithError(webtransgo.SessionErrorCode(0), "")
		return nil, err
	}
	return ts, nil
}

// Dialは、デフォルト設定を使ってトランスポート接続します。
func Dial(c transport.DialConfig) (transport.Transport, error) {
	return DialWithConfig(c, defaultDialerConfig)
}

// DialWithConfig は、指定された設定でトランスポート接続を開始します。
func DialWithConfig(c transport.DialConfig, cc DialerConfig) (transport.Transport, error) {
	d := &Dialer{
		DialerConfig: cc,
	}
	return d.Dial(c)
}
