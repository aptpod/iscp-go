package wire

import "sync/atomic"

type AliasGenerator struct {
	currentValue uint32
}

func (g *AliasGenerator) Next() uint32 {
	for {
		n := atomic.AddUint32(&g.currentValue, 1)
		if n != 0 {
			return n
		}
	}
}

func (g *AliasGenerator) CurrentValue() uint32 {
	return atomic.LoadUint32(&g.currentValue)
}

func NewAliasGenerator(initial uint32) *AliasGenerator {
	return &AliasGenerator{currentValue: initial}
}
