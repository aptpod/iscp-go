package log_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	. "github.com/aptpod/iscp-go/log"
)

func Test_nopLogger(t *testing.T) {
	testee := NewNop()
	ctx := context.Background()
	require.NotPanics(t, func() { testee.Infof(ctx, "message") })
	require.NotPanics(t, func() { testee.Warnf(ctx, "message") })
	require.NotPanics(t, func() { testee.Errorf(ctx, "message") })
	require.NotPanics(t, func() { testee.Debugf(ctx, "message") })
}
