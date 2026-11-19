package iscp

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestResendScheduler_Waits(t *testing.T) {
	tests := []struct {
		name        string
		ackInterval time.Duration
		// wantWaits are the waits before the 1st, 2nd, ... resend.
		wantWaits []time.Duration
	}{
		{
			name:        "default ack interval",
			ackInterval: 100 * time.Millisecond,
			wantWaits:   []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second, 60 * time.Second, 60 * time.Second},
		},
		{
			name:        "twice the ack interval exceeds 5s",
			ackInterval: 4 * time.Second,
			wantWaits:   []time.Duration{8 * time.Second, 16 * time.Second, 32 * time.Second, 60 * time.Second},
		},
		{
			name:        "first wait exceeds 60s",
			ackInterval: 40 * time.Second,
			wantWaits:   []time.Duration{80 * time.Second, 80 * time.Second},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newResendScheduler(tt.ackInterval)
			now := time.Unix(1000, 0)
			s.schedule(7, now)
			for i, want := range tt.wantWaits {
				due, ok := s.nextDue()
				assert.True(t, ok)
				assert.Equal(t, want, due.Sub(now), "wait before resend %d", i+1)
				assert.Empty(t, s.popDue(due.Add(-time.Nanosecond)), "not due yet")
				assert.Equal(t, []uint32{7}, s.popDue(due))
				now = due
			}
		})
	}
}

func TestResendScheduler_RemoveAndReset(t *testing.T) {
	s := newResendScheduler(100 * time.Millisecond)
	now := time.Unix(1000, 0)
	s.schedule(1, now)
	s.schedule(2, now.Add(time.Second))
	// Scheduling an existing entry keeps its due time.
	s.schedule(1, now.Add(3*time.Second))

	due, ok := s.nextDue()
	assert.True(t, ok)
	assert.Equal(t, now.Add(5*time.Second), due)

	s.remove(1)
	due, ok = s.nextDue()
	assert.True(t, ok)
	assert.Equal(t, now.Add(6*time.Second), due)

	s.reset()
	_, ok = s.nextDue()
	assert.False(t, ok)
	assert.Empty(t, s.popDue(now.Add(time.Hour)))
}
