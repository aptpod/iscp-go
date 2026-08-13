package webtransport_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/aptpod/iscp-go/v2/transport/webtransport"
)

// TestDialerConfig_QUICConfigは、DialerConfigのタイムアウト設定がquic-goの設定へ
// 渡ること、および未設定時にquic-goの既定値へ委ねられることを確認します。
func TestDialerConfig_QUICConfig(t *testing.T) {
	t.Run("設定した値が渡る", func(t *testing.T) {
		got := webtransport.DialerConfig{
			MaxIdleTimeout:  3 * time.Second,
			KeepAlivePeriod: time.Second,
		}.QUICConfig()

		assert.Equal(t, 3*time.Second, got.MaxIdleTimeout)
		assert.Equal(t, time.Second, got.KeepAlivePeriod)
	})

	t.Run("未設定ならquic-goの既定値に委ねる", func(t *testing.T) {
		got := webtransport.DialerConfig{}.QUICConfig()

		assert.Zero(t, got.MaxIdleTimeout)
		assert.Zero(t, got.KeepAlivePeriod)
	})

	t.Run("既存の設定は維持される", func(t *testing.T) {
		got := webtransport.DialerConfig{MaxIdleTimeout: time.Second}.QUICConfig()

		assert.True(t, got.EnableDatagrams)
		assert.True(t, got.EnableStreamResetPartialDelivery)
	})
}
