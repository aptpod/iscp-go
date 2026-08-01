package quic_test

import (
	"context"
	"strings"
	"testing"
	"time"

	quicgo "github.com/quic-go/quic-go"
	"github.com/stretchr/testify/require"

	"github.com/aptpod/iscp-go/v2/internal/testdata"
	"github.com/aptpod/iscp-go/v2/transport"
	. "github.com/aptpod/iscp-go/v2/transport/quic"
)

// TestDialer_DialContext_negotiateのWriteがctxで中断できる は、QUIC ハンド
// シェイク後の negotiate（uni stream への params 書き込み）がフロー制御の
// 枯渇でブロックしても、ctx のキャンセルで DialContext が返ることを検証する。
//
// stream.Write を実際にブロックさせる構成:
//   - quic-go は書き込み残量が MaxPacketBufferSize（1452 バイト）以下なら
//     フレームバッファへ複写して即 return するため、既定のパラメータサイズ
//     （数百バイト）では Write はブロックしない。SuperConnectionID は長さ
//     検証のない公開 API（transport.DialConfig）なので、これを 1452 バイト
//     と flow control window の双方より大きくしてブロック分岐へ入れる。
//   - サーバーは接続を受けるだけで uni stream を Accept しない（読まない）
//     ため、credit は初期 window（4KiB に絞る）から増えない。
func TestDialer_DialContext_negotiateのWriteがctxで中断できる(t *testing.T) {
	tlsConfig := testdata.GetTLSConfig()
	tlsConfig.NextProtos = []string{"iscp"}
	lis, err := quicgo.ListenAddr("localhost:0", tlsConfig, &quicgo.Config{
		InitialStreamReceiveWindow:     4096,
		MaxStreamReceiveWindow:         4096,
		InitialConnectionReceiveWindow: 4096,
		MaxConnectionReceiveWindow:     4096,
	})
	require.NoError(t, err)
	defer lis.Close()

	// サーバーは接続を受けたことを通知するだけで、stream は読まない。
	accepted := make(chan struct{})
	srvCtx, srvCancel := context.WithCancel(context.Background())
	defer srvCancel()
	srvDone := make(chan struct{})
	go func() {
		defer close(srvDone)
		sess, err := lis.Accept(srvCtx)
		if err != nil {
			return
		}
		defer sess.CloseWithError(0, "test done")
		close(accepted)
		<-srvCtx.Done()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d := NewDialer(DialerConfig{TLSConfig: testdata.GetTLSConfig()})
	done := make(chan error, 1)
	go func() {
		_, err := d.DialContext(ctx, transport.DialConfig{
			Address:           lis.Addr().String(),
			SuperConnectionID: transport.SuperConnectionID(strings.Repeat("x", 256*1024)),
		})
		done <- err
	}()

	// ハンドシェイク完了を待ってからキャンセルする。100ms の猶予は negotiate
	// の Write がブロック状態に入るのを待つベストエフォート（キャンセルが
	// Write より先に届いた場合も、AfterFunc 済みの deadline で Write が即
	// エラーになるため、検証内容は変わらない）。
	select {
	case <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not accept the connection")
	}
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		require.Error(t, err)
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(3 * time.Second):
		t.Fatal("DialContext did not return after ctx cancellation: negotiate write is not ctx-aware")
	}
}
