package multi

import "time"

// Constants used by the ECF (Earliest Completion First) scheduling algorithm.

const (
	// ecfBeta is the beta constant of the ECF algorithm.
	// It is used in the second inequality and determines the threshold for deciding to wait.
	// The larger the value, the more readily the algorithm waits.
	ecfBeta = 4

	// mss is the Maximum Segment Size.
	// It is the maximum payload size (in bytes) of a typical TCP segment.
	// It is the Ethernet MTU of 1500 bytes minus the IP/TCP headers (20+20=40 bytes).
	//nolint:unused // Exported for tests in export_test.go.
	mss = 1460

	// defaultCWND is the default congestion window size in bytes.
	// It is 10 segments, the initial congestion window recommended by RFC 6928 for TCP connections.
	// It is used as a fallback value when metrics cannot be obtained.
	//nolint:unused // Exported for tests in export_test.go.
	defaultCWND = 10 * mss // 14,600 bytes
)

// abs returns the absolute value of a time.Duration.
//
//nolint:unused // Exported for tests in export_test.go.
func abs(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// rttToMicroseconds converts a time.Duration to a uint64 in microseconds.
// It is used in the calculations of the ECF algorithm.
func rttToMicroseconds(d time.Duration) uint64 {
	return uint64(d.Microseconds())
}

// microsecondsToRTT converts a uint64 in microseconds to a time.Duration.
//
//nolint:unused // Exported for tests in export_test.go.
func microsecondsToRTT(us uint64) time.Duration {
	return time.Duration(us) * time.Microsecond
}
