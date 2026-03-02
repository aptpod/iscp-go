package multi_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/aptpod/iscp-go/v2/transport"
	. "github.com/aptpod/iscp-go/v2/transport/multi"
)

func TestRoundRobinSelector(t *testing.T) {
	t.Run("正常なラウンドロビン", func(t *testing.T) {
		transportIDs := []transport.SubConnectionID{"transport1", "transport2", "transport3"}
		selector := NewRoundRobinSelector(transportIDs)

		// 1周目
		assert.Equal(t, transport.SubConnectionID("transport1"), selector.Get(0))
		assert.Equal(t, transport.SubConnectionID("transport2"), selector.Get(0))
		assert.Equal(t, transport.SubConnectionID("transport3"), selector.Get(0))

		// 2周目
		assert.Equal(t, transport.SubConnectionID("transport1"), selector.Get(0))
		assert.Equal(t, transport.SubConnectionID("transport2"), selector.Get(0))
		assert.Equal(t, transport.SubConnectionID("transport3"), selector.Get(0))
	})

	t.Run("空のSubConnectionIDs", func(t *testing.T) {
		selector := NewRoundRobinSelector([]transport.SubConnectionID{})
		assert.Equal(t, transport.SubConnectionID(""), selector.Get(0))
	})

	t.Run("単一のSubConnectionID", func(t *testing.T) {
		selector := NewRoundRobinSelector([]transport.SubConnectionID{"transport1"})
		assert.Equal(t, transport.SubConnectionID("transport1"), selector.Get(0))
		assert.Equal(t, transport.SubConnectionID("transport1"), selector.Get(0))
		assert.Equal(t, transport.SubConnectionID("transport1"), selector.Get(0))
	})

	t.Run("並行アクセス", func(t *testing.T) {
		transportIDs := []transport.SubConnectionID{"transport1", "transport2", "transport3"}
		selector := NewRoundRobinSelector(transportIDs)

		done := make(chan bool)
		go func() {
			for i := 0; i < 100; i++ {
				selector.Get(0)
			}
			done <- true
		}()
		go func() {
			for i := 0; i < 100; i++ {
				selector.Get(0)
			}
			done <- true
		}()

		<-done
		<-done
	})
}
