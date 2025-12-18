package multi

import (
	"time"

	"github.com/aptpod/iscp-go/transport"
)

// ECF定数のエクスポート（テスト用）
const (
	EcfBeta     = ecfBeta
	MSS         = mss
	DefaultCWND = defaultCWND
)

// ECFヘルパー関数のエクスポート（テスト用）
func Abs(d time.Duration) time.Duration {
	return abs(d)
}

func RTTToMicroseconds(d time.Duration) uint64 {
	return rttToMicroseconds(d)
}

func MicrosecondsToRTT(us uint64) time.Duration {
	return microsecondsToRTT(us)
}

// SelectTransportECF は selectTransportECF をテスト用にエクスポートします。
func (s *ECFSelector) SelectTransportECF() transport.TransportID {
	return s.selectTransportECF()
}
