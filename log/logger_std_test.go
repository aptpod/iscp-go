package log_test

import (
	"context"
	"log"
	"os"
	"testing"

	. "github.com/aptpod/iscp-go/log"
	"github.com/stretchr/testify/require"
)

func Test_stdLogger(t *testing.T) {
	testee := NewStd()
	ctx := context.Background()
	require.NotPanics(t, func() { testee.Infof(ctx, "message") })
	require.NotPanics(t, func() { testee.Warnf(ctx, "message") })
	require.NotPanics(t, func() { testee.Errorf(ctx, "message") })
	require.NotPanics(t, func() { testee.Debugf(ctx, "message") })
}

func Example_stdLogger() {
	ctx := context.Background()
	testee := NewStd()
	log.SetOutput(os.Stdout)
	log.SetFlags(log.Lshortfile)
	testee.Infof(ctx, "message %s", "info")
	testee.Warnf(ctx, "message %s", "warn")
	testee.Errorf(ctx, "message %s", "error")
	testee.Debugf(ctx, "message %s", "debug")

	// Output:
	// logger_std_test.go:27: INFO: message info
	// logger_std_test.go:28: WARN: message warn
	// logger_std_test.go:29: ERROR: message error
	// logger_std_test.go:30: DEBUG: message debug
}
