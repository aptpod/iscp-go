package webtransport_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/aptpod/iscp-go/v2/transport/webtransport"
)

// TestDialerConfig_QUICConfig verifies that the timeout settings in DialerConfig are
// passed to the quic-go config, and that quic-go defaults apply when they are unset.
func TestDialerConfig_QUICConfig(t *testing.T) {
	t.Run("passes the configured values", func(t *testing.T) {
		got := webtransport.DialerConfig{
			MaxIdleTimeout:  3 * time.Second,
			KeepAlivePeriod: time.Second,
		}.QUICConfig()

		assert.Equal(t, 3*time.Second, got.MaxIdleTimeout)
		assert.Equal(t, time.Second, got.KeepAlivePeriod)
	})

	t.Run("defers to quic-go defaults when unset", func(t *testing.T) {
		got := webtransport.DialerConfig{}.QUICConfig()

		assert.Zero(t, got.MaxIdleTimeout)
		assert.Zero(t, got.KeepAlivePeriod)
	})

	t.Run("keeps the existing settings", func(t *testing.T) {
		got := webtransport.DialerConfig{MaxIdleTimeout: time.Second}.QUICConfig()

		assert.True(t, got.EnableDatagrams)
		assert.True(t, got.EnableStreamResetPartialDelivery)
	})
}
