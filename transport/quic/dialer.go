package quic

import (
	"context"
	"crypto/tls"

	quicgo "github.com/quic-go/quic-go"

	"github.com/aptpod/iscp-go/errors"
	"github.com/aptpod/iscp-go/transport"
)

const (
	defaultQueueSize = 32
)

var defaultDialerConfig = DialerConfig{
	QueueSize: defaultQueueSize,
	TLSConfig: &tls.Config{
		NextProtos: []string{"iscp"},
	},
}

// DialerConfigは、Dialerの設定です。
type DialerConfig struct {
	// QueueSize は、トランスポートとメッセージをやり取りする際のメッセージキューの長さです。
	// 0 に設定された場合は、 DefaultQueueSize の値が使用されます。
	QueueSize int

	// TLSConfigは、TLS接続の設定です。
	//
	// TLSConfig.NextProtosは必ず、`iscp` に上書きします。
	TLSConfig *tls.Config
}

// Dialerは、QUICのトランスポートを接続します。
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
func (d *Dialer) Dial(c transport.DialConfig) (transport.Transport, error) {
	ctx := context.Background()

	if d.TLSConfig == nil {
		d.TLSConfig = defaultDialerConfig.TLSConfig
	} else {
		d.TLSConfig.NextProtos = []string{"iscp"}
	}
	sess, err := quicgo.DialAddr(ctx, c.Address, d.TLSConfig, &quicgo.Config{
		EnableDatagrams: true,
	})
	if err != nil {
		return nil, err
	}

	params, err := d.negotiate(c, sess)
	if err != nil {
		return nil, errors.Errorf("negotiation failed: %w", err)
	}

	ts, err := New(Config{
		Connection:        sess,
		QueueSize:         d.QueueSize,
		CompressConfig:    c.CompressConfig,
		NegotiationParams: *params,
	})
	if err != nil {
		return nil, err
	}

	return ts, nil
}

func (d *Dialer) negotiate(c transport.DialConfig, sess quicgo.Connection) (*NegotiationParams, error) {
	stream, err := sess.OpenUniStream()
	if err != nil {
		return nil, err
	}
	defer stream.Close()

	params := &NegotiationParams{c.NegotiationParams()}
	b, err := params.Marshal()
	if err != nil {
		return nil, err
	}

	if _, err := stream.Write(b); err != nil {
		return nil, err
	}

	return params, nil
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
