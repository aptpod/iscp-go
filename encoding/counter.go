package encoding

import (
	"reflect"
	"sync"

	"github.com/aptpod/iscp-go/message"
)

type counter struct {
	sync.RWMutex
	byteCount    map[reflect.Type]uint64
	messageCount map[reflect.Type]uint64
}

func newCounter() *counter {
	return &counter{
		byteCount:    map[reflect.Type]uint64{},
		messageCount: map[reflect.Type]uint64{},
	}
}

func (c *counter) Add(msg message.Message, bytes int) {
	c.Lock()
	defer c.Unlock()
	typ := reflect.TypeOf(msg)
	c.messageCount[typ] = c.messageCount[typ] + 1
	c.byteCount[typ] = c.byteCount[typ] + uint64(bytes)
}

func (c *counter) Count() *Count {
	c.RLock()
	defer c.RUnlock()
	res := &Count{
		ByteCount:    make(map[reflect.Type]uint64, len(c.byteCount)),
		MessageCount: make(map[reflect.Type]uint64, len(c.messageCount)),
	}
	for k := range c.byteCount {
		res.ByteCount[k] = c.byteCount[k]
	}
	for k := range c.messageCount {
		res.MessageCount[k] = c.messageCount[k]
	}
	return res
}

// Countは、メッセージごとの送受信のバイト数とメッセージ数の数を表します。
type Count struct {
	ByteCount    map[reflect.Type]uint64
	MessageCount map[reflect.Type]uint64
}
