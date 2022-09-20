package wire

import (
	"testing"
	"time"

	"github.com/aptpod/iscp-go/message"
)

type nopWriter struct{}

func (w nopWriter) Write(msg message.Message) error {
	return nil
}

func (c *ClientConn) Done() <-chan struct{} {
	return c.ctx.Done()
}

func SetDefaultPingInterval(t *testing.T, d time.Duration) {
	org := defaultPingInterval
	defaultPingInterval = d
	t.Cleanup(func() {
		defaultPingInterval = org
	})
}

func SetDefaultPingTimeout(t *testing.T, d time.Duration) {
	org := defaultPingTimeout
	defaultPingTimeout = d

	t.Cleanup(func() {
		defaultPingTimeout = org
	})
}
