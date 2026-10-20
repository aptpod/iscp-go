package multi

import (
	"time"

	"github.com/aptpod/iscp-go/v2/transport"
)

// Export ECF constants (for tests).
const (
	EcfBeta     = ecfBeta
	MSS         = mss
	DefaultCWND = defaultCWND
)

// Export ECF helper functions (for tests).
func Abs(d time.Duration) time.Duration {
	return abs(d)
}

func RTTToMicroseconds(d time.Duration) uint64 {
	return rttToMicroseconds(d)
}

func MicrosecondsToRTT(us uint64) time.Duration {
	return microsecondsToRTT(us)
}

// SelectTransportECF exports selectTransportECF for tests.
// Tests always record stats (recordStats=true).
func (s *ECFSelector) SelectTransportECF() transport.SubConnectionID {
	return s.selectTransportECF(true)
}
