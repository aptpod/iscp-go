package quic_test

import (
	"context"
	"testing"

	quicgo "github.com/quic-go/quic-go"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/aptpod/iscp-go/internal/testdata"
	"github.com/aptpod/iscp-go/transport"
	. "github.com/aptpod/iscp-go/transport/quic"
)

// startNegotiationFailServerは、ユニストリームを一切受け付けないQUICサーバーを起動します。
// クライアントはハンドシェイク後のネゴシエーション（OpenUniStream）で必ず失敗します。
func startNegotiationFailServer(t testing.TB) (addr string, closeFunc func()) {
	t.Helper()
	tlsConfig := testdata.GetTLSConfig()
	tlsConfig.NextProtos = []string{"iscp"}
	lis, err := quicgo.ListenAddr("localhost:0", tlsConfig, &quicgo.Config{
		MaxIncomingUniStreams: -1,
	})
	require.NoError(t, err)

	ctx := context.Background()
	go func() {
		for {
			if _, err := lis.Accept(ctx); err != nil {
				return
			}
		}
	}()
	return lis.Addr().String(), func() { lis.Close() }
}

func TestDialer_Dial_NegotiationFailed_ClosesSession(t *testing.T) {
	addr, closeServer := startNegotiationFailServer(t)
	t.Cleanup(closeServer)

	d := NewDialer(DialerConfig{
		TLSConfig: testdata.GetTLSConfig(),
	})

	// Dial以降に生成されるgoroutineだけを検査対象にする。
	opt := goleak.IgnoreCurrent()

	_, err := d.Dial(transport.DialConfig{Address: addr})
	require.Error(t, err)

	// ネゴシエーション失敗時にQUICセッションが閉じられていれば、
	// セッションの内部goroutine（コネクションのrunループ等）は残らない。
	goleak.VerifyNone(t, opt)
}

// startTransportNewFailServerは、ユニストリームを1本だけ受け付けるQUICサーバーを起動します。
// ネゴシエーション用の1本目は成功しますが、New内で送信ストリームを開くための2本目が失敗します。
func startTransportNewFailServer(t testing.TB) (addr string, closeFunc func()) {
	t.Helper()
	tlsConfig := testdata.GetTLSConfig()
	tlsConfig.NextProtos = []string{"iscp"}
	lis, err := quicgo.ListenAddr("localhost:0", tlsConfig, &quicgo.Config{
		MaxIncomingUniStreams: 1,
	})
	require.NoError(t, err)

	ctx := context.Background()
	go func() {
		for {
			if _, err := lis.Accept(ctx); err != nil {
				return
			}
		}
	}()
	return lis.Addr().String(), func() { lis.Close() }
}

func TestDialer_Dial_NewTransportFailed_ClosesSession(t *testing.T) {
	addr, closeServer := startTransportNewFailServer(t)
	t.Cleanup(closeServer)

	d := NewDialer(DialerConfig{
		TLSConfig: testdata.GetTLSConfig(),
	})

	opt := goleak.IgnoreCurrent()

	_, err := d.Dial(transport.DialConfig{Address: addr})
	require.Error(t, err)

	// New内で送信ストリームのオープンに失敗した場合も、QUICセッションが閉じられていれば
	// セッションの内部goroutineは残らない。
	goleak.VerifyNone(t, opt)
}
