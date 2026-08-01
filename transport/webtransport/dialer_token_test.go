package webtransport_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aptpod/iscp-go/v2/transport"
	. "github.com/aptpod/iscp-go/v2/transport/webtransport"
)

// recordingTokenSource は Token / TokenWithContext のどちらが呼ばれたかを
// 記録する。両方を実装しているため transport.TokenSourceWithContext を満たす。
type recordingTokenSource struct {
	plainCalled atomic.Bool
	ctxCalled   atomic.Bool
}

func (r *recordingTokenSource) Token() (*transport.Token, error) {
	r.plainCalled.Store(true)
	return &transport.Token{Token: "tk"}, nil
}

func (r *recordingTokenSource) TokenWithContext(_ context.Context) (*transport.Token, error) {
	r.ctxCalled.Store(true)
	return &transport.Token{Token: "tk"}, nil
}

// ctxBlockingTokenSource は、TokenWithContext では ctx のキャンセルまで待って
// ctx.Err() を返し、Token では ctx を受け取れないことを表すエラーを返す。
// 「トークン取得が外部認証サーバーへの問い合わせでブロックする」状況の模擬。
type ctxBlockingTokenSource struct{}

func (s *ctxBlockingTokenSource) Token() (*transport.Token, error) {
	return nil, assert.AnError
}

func (s *ctxBlockingTokenSource) TokenWithContext(ctx context.Context) (*transport.Token, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestDialer_DialContext_TokenSourceWithContextを優先する は、TokenSource が
// TokenSourceWithContext を実装している場合、DialContext が ctx 付きの
// TokenWithContext を呼ぶことを検証する。
func TestDialer_DialContext_TokenSourceWithContextを優先する(t *testing.T) {
	src := &recordingTokenSource{}
	d := NewDialer(DialerConfig{TokenSource: src})

	// dial 自体は成立しなくてよい（トークン取得の呼び分けだけを見る）。
	// キャンセル済み ctx で HTTP/3 CONNECT を即中断させる。
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := d.DialContext(ctx, transport.DialConfig{Address: "127.0.0.1:1"})
	require.Error(t, err)

	assert.True(t, src.ctxCalled.Load(), "TokenWithContext should be called for TokenSourceWithContext implementations")
	assert.False(t, src.plainCalled.Load(), "plain Token() should not be called when TokenWithContext is available")
}

// TestDialer_DialContext_Token取得がctxで打ち切れる は、トークン取得が
// ブロックしても dial の ctx で打ち切れることを検証する。
func TestDialer_DialContext_Token取得がctxで打ち切れる(t *testing.T) {
	d := NewDialer(DialerConfig{TokenSource: &ctxBlockingTokenSource{}})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := d.DialContext(ctx, transport.DialConfig{Address: "127.0.0.1:1"})
	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(start), 3*time.Second)
}
