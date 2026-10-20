package transport_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aptpod/iscp-go/v2/transport"
)

// StaticTokenSource implements TokenSourceWithContext (it always returns
// immediately, so ctx is ignored).
var _ transport.TokenSourceWithContext = (*transport.StaticTokenSource)(nil)

func TestStaticTokenSource_TokenWithContext(t *testing.T) {
	ts := &transport.StaticTokenSource{StaticToken: &transport.Token{Token: "tk"}}
	got, err := ts.TokenWithContext(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "tk", got.Token)
}
