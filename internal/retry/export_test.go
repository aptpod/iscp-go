package retry

import "testing"

var NextSleep = nextSleep

func SetRandFloat64(t *testing.T, f float64) {
	org := randFloat64
	randFloat64 = func() float64 { return f }
	t.Cleanup(func() {
		randFloat64 = org
	})
}
