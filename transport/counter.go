package transport

import (
	"reflect"
	"sync"
	"sync/atomic"

	"github.com/aptpod/iscp-go/message"
)

// typeCounter はメッセージ種別ごとのカウンターです。atomic操作でロックフリーに更新します。
type typeCounter struct {
	byteCount    atomic.Uint64
	messageCount atomic.Uint64
}

type counter struct {
	mu    sync.RWMutex
	types map[reflect.Type]*typeCounter
}

func newCounter() *counter {
	return &counter{
		types: make(map[reflect.Type]*typeCounter),
	}
}

// Add はメッセージの送受信を記録します。
// ホットパス: ウォームアップ後は RLock + map lookup + 2x atomic add のみ（ゼロアロケーション）。
func (c *counter) Add(msg message.Message, bytes int) {
	typ := reflect.TypeOf(msg)

	// Fast path: 型が登録済み（通常ケース）
	c.mu.RLock()
	tc, ok := c.types[typ]
	c.mu.RUnlock()

	if !ok {
		// Slow path: 初回のみ（double-checked locking）
		c.mu.Lock()
		tc, ok = c.types[typ]
		if !ok {
			tc = &typeCounter{}
			c.types[typ] = tc
		}
		c.mu.Unlock()
	}

	tc.messageCount.Add(1)
	tc.byteCount.Add(uint64(bytes))
}

// messageTypeName は reflect.Type から簡潔な型名を返します。
// e.g. *message.UpstreamOpenRequest → "UpstreamOpenRequest"
func messageTypeName(typ reflect.Type) string {
	if typ.Kind() == reflect.Ptr {
		return typ.Elem().Name()
	}
	return typ.Name()
}

// Count はメッセージ種別ごとのカウントのスナップショットを返します。
// コールドパス: string変換はここでのみ行われる。
func (c *counter) Count() *Count {
	c.mu.RLock()
	defer c.mu.RUnlock()
	res := &Count{
		ByteCount:    make(map[string]uint64, len(c.types)),
		MessageCount: make(map[string]uint64, len(c.types)),
	}
	for typ, tc := range c.types {
		key := messageTypeName(typ)
		res.ByteCount[key] = tc.byteCount.Load()
		res.MessageCount[key] = tc.messageCount.Load()
	}
	return res
}

// Count はメッセージ種別ごとの送受信のバイト数とメッセージ数を表します。
type Count struct {
	ByteCount    map[string]uint64
	MessageCount map[string]uint64
}
