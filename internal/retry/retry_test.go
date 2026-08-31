package retry_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	. "github.com/aptpod/iscp-go/internal/retry"
)

func TestRetry_nextSleep(t *testing.T) {
	type args struct {
		count           int
		baseInterval    time.Duration
		maxBaseInterval time.Duration
	}
	tests := []struct {
		name string
		args args
		want time.Duration
	}{
		{
			name: "success:0",
			args: args{count: 0, baseInterval: 1, maxBaseInterval: 1000},
			want: 1,
		},
		{
			name: "success:1",
			args: args{count: 1, baseInterval: 1, maxBaseInterval: 1000},
			want: 2,
		},
		{
			name: "success:2",
			args: args{count: 2, baseInterval: 1, maxBaseInterval: 1000},
			want: 4,
		},
		{
			name: "success:5",
			args: args{count: 5, baseInterval: 1, maxBaseInterval: 1000},
			want: 32,
		},
		{
			name: "success:7",
			args: args{count: 7, baseInterval: 1, maxBaseInterval: 1000},
			want: 128,
		},
		{
			name: "success:100",
			args: args{count: 100000, baseInterval: 1, maxBaseInterval: 1000},
			want: 1000,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			SetRandFloat64(t, 0.5)
			assert.Equal(t, tt.want, NextSleep(tt.args.count, tt.args.baseInterval, tt.args.maxBaseInterval))
		})
	}
}

func TestRetry_Do(t *testing.T) {
	for i := 0; i < 5; i++ {
		start := time.Now()
		var count int
		Do(func() (end bool) {
			count++
			return count == 3
		})
		assert.Greater(t, time.Since(start), time.Millisecond*150)
		assert.Less(t, time.Since(start), time.Millisecond*450)
	}
}

func TestRetry_DoWithContext_CanceledBeforeCall(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var called bool
	DoWithContext(ctx, func() (end bool) {
		called = true
		return true
	})
	assert.False(t, called)
}

func TestRetry_DoWithContext_CancelDuringSleep(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := Retry{BaseInterval: time.Second}

	var count int
	start := time.Now()
	done := make(chan struct{})
	go func() {
		r.DoWithContext(ctx, func() (end bool) {
			count++
			return false
		})
		close(done)
	}()

	time.Sleep(10 * time.Millisecond)
	cancel()
	<-done

	assert.Equal(t, 1, count)
	assert.Less(t, time.Since(start), time.Millisecond*100)
}

func TestRetry_DoWithContext_MaxAttempt(t *testing.T) {
	SetRandFloat64(t, 0)
	r := Retry{MaxAttempt: 2, BaseInterval: time.Millisecond}

	var count int
	r.DoWithContext(context.Background(), func() (end bool) {
		count++
		return false
	})
	assert.Equal(t, 3, count)
}
