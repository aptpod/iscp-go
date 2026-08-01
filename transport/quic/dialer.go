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
	// MetricsProvider を生成し、qlog Tracer を quic.Config に注入する。
	// Provider のライフサイクルは Dial 成功後に Transport に移譲する。
	provider := metrics.NewQUICMetricsProvider()
	sess, err := quicgo.DialAddr(ctx, c.Address, d.TLSConfig, &quicgo.Config{
		EnableDatagrams:                  true,
		EnableStreamResetPartialDelivery: true,
		Tracer:                           provider.Tracer(),
	})
	if err != nil {
		return nil, err
	}

	params, err := d.negotiate(ctx, c, sess)
	if err != nil {
		// 失敗した dial の QUIC セッションはここで畳む。閉じないと
		// quic-go のセッション goroutine がリークする。
		_ = sess.CloseWithError(0, "negotiation failed")
		return nil, errors.Errorf("negotiation failed: %w", err)
	}

	// v4: V4Transport が圧縮を担当するため base の圧縮を無効化
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
	// OpenUniStream は非ブロッキング（ストリーム上限到達時は即エラー）。
	// ブロックするのは OpenUniStreamSync のほう。
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

	// stream.Write は ctx を見ず、相手が読まずフロー制御の credit が尽きる
	// と無期限にブロックする。quic-go は書き込み残量が 1452 バイト
	// （MaxPacketBufferSize）以下ならフレームバッファへ複写して即 return
	// するため既定のパラメータサイズでは実際にはブロックしないが、その
	// 有界性は quic-go の内部実装とパラメータ長（SuperConnectionID 等は
	// 長さ検証がない）という暗黙の不変条件に依存する。ctx のキャンセル・
	// 期限で write deadline を過去に落として抜けさせる。
	// CancelWrite を使わないのは、Write 成功直後に ctx がキャンセルされた
	// 場合に RESET_STREAM が送信済みデータごと破棄してしまう競合がある
	// ため（SetWriteDeadline はブロック中・以降の Write にしか効かない）。
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
