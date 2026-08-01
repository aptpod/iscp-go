package transport

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errDialerTestSentinel = errors.New("dialer test sentinel")

// legacyOnlyDialer は Dial のみを実装する従来型の Dialer。
type legacyOnlyDialer struct {
	dialCalled bool
}

func (d *legacyOnlyDialer) Dial(DialConfig) (Transport, error) {
	d.dialCalled = true
	return nil, errDialerTestSentinel
}

// contextAwareDialer は ContextDialer を実装する Dialer。
type contextAwareDialer struct {
	dialCalled        bool
	dialContextCalled bool
	gotCtx            context.Context
}

func (d *contextAwareDialer) Dial(DialConfig) (Transport, error) {
	d.dialCalled = true
	return nil, errDialerTestSentinel
}

func (d *contextAwareDialer) DialContext(ctx context.Context, _ DialConfig) (Transport, error) {
	d.dialContextCalled = true
	d.gotCtx = ctx
	return nil, errDialerTestSentinel
}

// TestDialWithContext_ContextDialer実装ならDialContextが呼ばれる は、
// DialWithContext が型アサーションで ContextDialer を検出し、渡した ctx を
// そのまま伝えることを検証する。
func TestDialWithContext_ContextDialer実装ならDialContextが呼ばれる(t *testing.T) {
	d := &contextAwareDialer{}
	type ctxKey struct{}
	ctx := context.WithValue(context.Background(), ctxKey{}, "marker")

	_, err := DialWithContext(ctx, d, DialConfig{})

	require.ErrorIs(t, err, errDialerTestSentinel)
	assert.True(t, d.dialContextCalled)
	assert.False(t, d.dialCalled)
	assert.Equal(t, "marker", d.gotCtx.Value(ctxKey{}))
}

// TestDialWithContext_従来のDialerはDialへフォールバックする は、Dial のみを
// 実装する既存 Dialer が無改変で DialWithContext 経由でも動くことを検証する。
func TestDialWithContext_従来のDialerはDialへフォールバックする(t *testing.T) {
	d := &legacyOnlyDialer{}

	_, err := DialWithContext(context.Background(), d, DialConfig{})

	require.ErrorIs(t, err, errDialerTestSentinel)
	assert.True(t, d.dialCalled)
}
