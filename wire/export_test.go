package wire

import (
	"github.com/aptpod/iscp-go/v2/message"
)

type nopWriter struct{}

func (w nopWriter) Write(msg message.Message) error {
	return nil
}

func (c *ClientConn) Done() <-chan struct{} {
	return c.ctx.Done()
}

// IsAcceptableProtocolVersion は、isAcceptableProtocolVersion をテスト用にエクスポートします。
func IsAcceptableProtocolVersion(version string) bool {
	return isAcceptableProtocolVersion(version)
}
