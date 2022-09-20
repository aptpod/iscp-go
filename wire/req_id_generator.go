package wire

import (
	"sync/atomic"
)

// IDGeneratorは、リクエストIDのジェネレータです。
type IDGenerator struct {
	currentValue uint32
}

// NewIDGeneratorは、ジェネレータを返却します。
//
// initialには、 `Next` で最初に返却する値を指定します。
func NewIDGenerator(initial uint32) *IDGenerator {
	return &IDGenerator{
		currentValue: initial,
	}
}

// Nextは、次の値を返却します。
//
// 値は2ずつ加算します。
func (g *IDGenerator) Next() uint32 {
	return atomic.AddUint32(&g.currentValue, 2) - 2
}

func newRequestIDGeneratorForClient() IDGenerator {
	return IDGenerator{currentValue: 0}
}
