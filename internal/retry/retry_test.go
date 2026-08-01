package retry_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	. "github.com/aptpod/iscp-go/v2/internal/retry"
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

func TestRetry_DoWithContext_CancelDuringSleep(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := Retry{BaseInterval: 10 * time.Second}
	var count int
	start := time.Now()
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	r.DoWithContext(ctx, func() (end bool) {
		count++
		return false
	})
	// スリープ（10秒）中のキャンセルが即座に反映されること
	assert.Less(t, time.Since(start), time.Second)
	assert.Equal(t, 1, count)
}

func TestRetry_DoWithContext_CanceledBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var count int
	DoWithContext(ctx, func() (end bool) {
		count++
		return false
	})
	// キャンセル済みなら f を一度も呼ばない
	assert.Equal(t, 0, count)
}
