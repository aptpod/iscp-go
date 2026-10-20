package transport

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errDialerTestSentinel = errors.New("dialer test sentinel")

// legacyOnlyDialer is a legacy Dialer that implements only Dial.
type legacyOnlyDialer struct {
	dialCalled bool
}

func (d *legacyOnlyDialer) Dial(DialConfig) (Transport, error) {
	d.dialCalled = true
	return nil, errDialerTestSentinel
}

// contextAwareDialer is a Dialer that implements ContextDialer.
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

// TestDialWithContext_CallsDialContextWhenContextDialerImplemented verifies that
// DialWithContext detects ContextDialer by type assertion and passes the given
// ctx through unchanged.
func TestDialWithContext_CallsDialContextWhenContextDialerImplemented(t *testing.T) {
	d := &contextAwareDialer{}
	type ctxKey struct{}
	ctx := context.WithValue(context.Background(), ctxKey{}, "marker")

	_, err := DialWithContext(ctx, d, DialConfig{})

	require.ErrorIs(t, err, errDialerTestSentinel)
	assert.True(t, d.dialContextCalled)
	assert.False(t, d.dialCalled)
	assert.Equal(t, "marker", d.gotCtx.Value(ctxKey{}))
}

// TestDialWithContext_LegacyDialerFallsBackToDial verifies that an existing
// Dialer implementing only Dial also works via DialWithContext without modification.
func TestDialWithContext_LegacyDialerFallsBackToDial(t *testing.T) {
	d := &legacyOnlyDialer{}

	_, err := DialWithContext(context.Background(), d, DialConfig{})

	require.ErrorIs(t, err, errDialerTestSentinel)
	assert.True(t, d.dialCalled)
}
