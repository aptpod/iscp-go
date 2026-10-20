package websocket_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aptpod/iscp-go/v2/transport"
	. "github.com/aptpod/iscp-go/v2/transport/websocket"
)

// recordingTokenSource records which of Token / TokenWithContext was called.
// It implements both, so it satisfies transport.TokenSourceWithContext.
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

// ctxBlockingTokenSource waits until ctx is canceled in TokenWithContext and returns
// ctx.Err(), and in Token returns an error indicating that it cannot receive a ctx.
type ctxBlockingTokenSource struct{}

func (s *ctxBlockingTokenSource) Token() (*transport.Token, error) {
	return nil, assert.AnError
}

func (s *ctxBlockingTokenSource) TokenWithContext(ctx context.Context) (*transport.Token, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestDialer_DialContext_PrefersTokenSourceWithContext verifies that when the TokenSource
// implements TokenSourceWithContext, DialContext calls TokenWithContext with the ctx.
// It injects a DialFunc so as not to use the network.
func TestDialer_DialContext_PrefersTokenSourceWithContext(t *testing.T) {
	src := &recordingTokenSource{}
	d := NewDialer(DialerConfig{
		TokenSource: src,
		DialFunc: func(DialConfig) (Conn, error) {
			return nil, assert.AnError
		},
	})

	_, err := d.DialContext(context.Background(), transport.DialConfig{Address: "127.0.0.1:1"})
	require.Error(t, err)

	assert.True(t, src.ctxCalled.Load(), "TokenWithContext should be called for TokenSourceWithContext implementations")
	assert.False(t, src.plainCalled.Load(), "plain Token() should not be called when TokenWithContext is available")
}

// TestDialer_DialContext_TokenFetchCanBeAbortedByContext verifies that even if the token
// retrieval blocks, it can be aborted by the dial's ctx.
func TestDialer_DialContext_TokenFetchCanBeAbortedByContext(t *testing.T) {
	d := NewDialer(DialerConfig{
		TokenSource: &ctxBlockingTokenSource{},
		DialFunc: func(DialConfig) (Conn, error) {
			return nil, assert.AnError
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := d.DialContext(ctx, transport.DialConfig{Address: "127.0.0.1:1"})
	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(start), 3*time.Second)
}
