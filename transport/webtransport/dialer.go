package webtransport

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/aptpod/iscp-go/errors"
	"github.com/aptpod/iscp-go/transport"
	quicgo "github.com/quic-go/quic-go"
	webtransgo "github.com/quic-go/webtransport-go"
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

// Dialは、トランスポートを接続します。
func (d *Dialer) Dial(c transport.DialConfig) (transport.Transport, error) {
	if d.TLSConfig == nil {
		d.TLSConfig = defaultDialerConfig.TLSConfig
	}
	dialer := &webtransgo.Dialer{
		TLSClientConfig: d.TLSConfig,
		QUICConfig: &quicgo.Config{
			EnableDatagrams: true,
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

// Dialは、トランスポート接続します。
func DialWithConfig(c transport.DialConfig, cc DialerConfig) (transport.Transport, error) {
	d := &Dialer{
		DialerConfig: cc,
	}
	return d.Dial(c)
}
