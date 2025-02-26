package multi_test

import (
	"testing"

	"github.com/aptpod/iscp-go/transport"
	. "github.com/aptpod/iscp-go/transport/multi"
	"github.com/stretchr/testify/assert"
)

func TestRoundRobinPoller(t *testing.T) {
	t.Run("正常なラウンドロビン", func(t *testing.T) {
		transportIDs := []transport.TransportID{"transport1", "transport2", "transport3"}
		poller := NewRoundRobinPoller(transportIDs)

		// 1周目
		assert.Equal(t, transport.TransportID("transport1"), poller.Get())
		assert.Equal(t, transport.TransportID("transport2"), poller.Get())
		assert.Equal(t, transport.TransportID("transport3"), poller.Get())

		// 2周目
		assert.Equal(t, transport.TransportID("transport1"), poller.Get())
		assert.Equal(t, transport.TransportID("transport2"), poller.Get())
		assert.Equal(t, transport.TransportID("transport3"), poller.Get())
	})

	t.Run("空のTransportIDs", func(t *testing.T) {
		poller := NewRoundRobinPoller([]transport.TransportID{})
		assert.Equal(t, transport.TransportID(""), poller.Get())
	})

	t.Run("単一のTransportID", func(t *testing.T) {
		poller := NewRoundRobinPoller([]transport.TransportID{"transport1"})
		assert.Equal(t, transport.TransportID("transport1"), poller.Get())
		assert.Equal(t, transport.TransportID("transport1"), poller.Get())
		assert.Equal(t, transport.TransportID("transport1"), poller.Get())
	})

	t.Run("並行アクセス", func(t *testing.T) {
		transportIDs := []transport.TransportID{"transport1", "transport2", "transport3"}
		poller := NewRoundRobinPoller(transportIDs)

		done := make(chan bool)
		go func() {
			for i := 0; i < 100; i++ {
				poller.Get()
			}
			done <- true
		}()
		go func() {
			for i := 0; i < 100; i++ {
				poller.Get()
			}
			done <- true
		}()

		<-done
		<-done
	})
}
