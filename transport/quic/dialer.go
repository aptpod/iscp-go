package quic

import (
	"context"
	"crypto/tls"
	"time"

	quicgo "github.com/quic-go/quic-go"

	"github.com/aptpod/iscp-go/v2/errors"
	"github.com/aptpod/iscp-go/v2/transport"
	"github.com/aptpod/iscp-go/v2/transport/compress"
	"github.com/aptpod/iscp-go/v2/transport/metrics"
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

	// MaxIdleTimeoutは、無通信のままコネクションを維持する最大時間です。
	// この時間を超えるとコネクションは切断され、ブロックしていた書き込みも解除されます。
	// 0 に設定された場合は、quic-goの既定値(30秒)が使用されます。
	MaxIdleTimeout time.Duration

	// KeepAlivePeriodは、keep-alive PINGの送信間隔です。
	// 0 に設定された場合は、keep-aliveは無効です。
	KeepAlivePeriod time.Duration
}

// quicConfig builds the quic-go config from DialerConfig.
func (c DialerConfig) quicConfig() *quicgo.Config {
	return &quicgo.Config{
		EnableDatagrams:                  true,
		EnableStreamResetPartialDelivery: true,
		MaxIdleTimeout:                   c.MaxIdleTimeout,
		KeepAlivePeriod:                  c.KeepAlivePeriod,
	}
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
// 注意: このメソッドは d.TLSConfig を変更するため、並行呼び出しは安全ではありません。
func (d *Dialer) Dial(c transport.DialConfig) (transport.Transport, error) {
	return d.DialContext(context.Background(), c)
}

// DialContextは、ctxを尊重してトランスポート接続を開始します。
// transport.ContextDialerの実装です。並行呼び出しに関する注意はDialと同じです。
//
// ctxとは独立に、quic-goのハンドシェイクタイムアウトも上限として機能します
// （HandshakeIdleTimeoutは既定5秒のアイドル上限で、ハンドシェイク全体は
// 最大その2倍で中断されます）。
func (d *Dialer) DialContext(ctx context.Context, c transport.DialConfig) (transport.Transport, error) {
	if d.TLSConfig == nil {
		d.TLSConfig = defaultDialerConfig.TLSConfig
	} else {
		d.TLSConfig.NextProtos = []string{"iscp"}
	}
	// Create a MetricsProvider and inject the qlog Tracer into quic.Config.
	// The Provider's lifecycle is handed over to the Transport after Dial succeeds.
	provider := metrics.NewQUICMetricsProvider()
	quicConf := d.quicConfig()
	quicConf.Tracer = provider.Tracer()
	sess, err := quicgo.DialAddr(ctx, c.Address, d.TLSConfig, quicConf)
	if err != nil {
		return nil, err
	}

	params, err := d.negotiate(ctx, c, sess)
	if err != nil {
		// Close the QUIC session of the failed dial here. Otherwise the
		// quic-go session goroutine leaks.
		_ = sess.CloseWithError(0, "negotiation failed")
		return nil, errors.Errorf("negotiation failed: %w", err)
	}

	// v4: V4Transport handles compression, so disable compression in the base
	if c.TransportType != "" {
		ts, err := New(Config{
			Connection:        sess,
			QueueSize:         d.QueueSize,
			CompressConfig:    compress.Config{},
			NegotiationParams: params.WithoutCompression(),
			MetricsProvider:   provider,
		})
		if err != nil {
			return nil, err
		}
		return transport.NewV4Transport(ts, *params, c.CompressConfig), nil
	}
	ts, err := New(Config{
		Connection:        sess,
		QueueSize:         d.QueueSize,
		CompressConfig:    c.CompressConfig,
		NegotiationParams: *params,
		MetricsProvider:   provider,
	})
	if err != nil {
		return nil, err
	}
	return ts, nil
}

func (d *Dialer) negotiate(ctx context.Context, c transport.DialConfig, sess *quicgo.Conn) (*transport.NegotiationParams, error) {
	// OpenUniStream is non-blocking (it returns an error immediately when the
	// stream limit is reached). It is OpenUniStreamSync that blocks.
	stream, err := sess.OpenUniStream()
	if err != nil {
		return nil, err
	}
	defer stream.Close()

	p := c.NegotiationParams()
	params := &p
	b, err := params.MarshalBinaryKeyValues()
	if err != nil {
		return nil, err
	}

	// stream.Write ignores ctx and blocks indefinitely if the peer does not read
	// and the flow-control credit runs out. quic-go copies into the frame buffer
	// and returns immediately when the remaining write size is at most 1452 bytes
	// (MaxPacketBufferSize), so it does not actually block with the default
	// parameter size, but that boundedness depends on the implicit invariants of
	// quic-go internals and the parameter length (SuperConnectionID etc. have no
	// length validation). On ctx cancellation or expiry, move the write deadline
	// into the past to break out.
	// CancelWrite is not used because, if ctx is canceled right after a successful
	// Write, RESET_STREAM can discard even the data already sent (a race;
	// SetWriteDeadline only affects Writes that are blocked or come later).
	stop := context.AfterFunc(ctx, func() {
		_ = stream.SetWriteDeadline(time.Now())
	})
	defer stop()

	if _, err := stream.Write(b); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
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
