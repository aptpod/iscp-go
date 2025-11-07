package log_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	. "github.com/aptpod/iscp-go/log"
)

func Test_genTrackID(t *testing.T) {
	for i := 0; i < 1000; i++ {
		require.Regexp(t, "^[0-9]{4}-[0-9]{4}-[0-9]{4}$", GenTrackID())
	}
}

func TestTrackTransportID(t *testing.T) {
	ctx := context.Background()
	require.Empty(t, TrackTransportID(ctx))
	ctx = WithTrackTransportID(ctx)
	require.Regexp(t, "^[0-9]{4}-[0-9]{4}-[0-9]{4}$", TrackTransportID(ctx))
}

func TestTrackMessageID(t *testing.T) {
	ctx := context.Background()
	require.Empty(t, TrackMessageID(ctx))
	ctx = WithTrackMessageID(ctx)
	require.Regexp(t, "^[0-9]{4}-[0-9]{4}-[0-9]{4}$", TrackMessageID(ctx))
}
