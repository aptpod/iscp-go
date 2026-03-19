package webtransport

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	quicgo "github.com/quic-go/quic-go"
	webtransgo "github.com/quic-go/webtransport-go"

	"github.com/aptpod/iscp-go/errors"
	"github.com/aptpod/iscp-go/transport"
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
	TokenSource TokenSource
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

// StaticTokenSource は transport.StaticTokenSource の型エイリアスです。
type StaticTokenSource = transport.StaticTokenSource

// Dialは、トランスポートを接続します。
func (d *Dialer) Dial(c transport.DialConfig) (transport.Transport, error) {
	if d.TLSConfig == nil {
		d.TLSConfig = defaultDialerConfig.TLSConfig
	}
	dialer := &webtransgo.Dialer{
		TLSClientConfig: d.TLSConfig,
		QUICConfig: &quicgo.Config{
			EnableDatagrams:                  true,
			EnableStreamResetPartialDelivery: true,
		},
	}

	params := NegotiationParams{c.NegotiationParams()}
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
		tk, err = d.TokenSource.Token()
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
	_, conn, err := dialer.Dial(context.Background(), webtransURL.String(), header)
	if err != nil {
		return nil, errors.Errorf("webtransport dialing failed on [%s]: %w", webtransURL.String(), err)
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
