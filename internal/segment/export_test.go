package segment

import (
	"testing"
	"time"
)

func SetMaxPayloadSize(t *testing.T, size int) {
	t.Helper()
	org := maxPayloadSize
	maxPayloadSize = size
	t.Cleanup(func() { maxPayloadSize = org })
}

func SetTimeNow(t *testing.T, now time.Time) {
	t.Helper()
	org := timeNow
	timeNow = func() time.Time { return now }
	t.Cleanup(func() { timeNow = org })
}
