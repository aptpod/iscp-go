package iscp

import (
	"context"
	"testing"
	"time"
)

func TestOrDone_ForwardsValues(t *testing.T) {
	ctx := context.Background()
	in := make(chan int, 3)
	in <- 1
	in <- 2
	in <- 3
	close(in)

	out := orDone(ctx, in)
	var results []int
	for v := range out {
		results = append(results, v)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 values, got %d", len(results))
	}
}

func TestOrDone_StopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	in := make(chan int)

	out := orDone(ctx, in)
	cancel()

	select {
	case _, ok := <-out:
		if ok {
			t.Fatal("expected channel to be closed")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for channel close")
	}
}

func TestOrDone_ClosesOnInputClose(t *testing.T) {
	ctx := context.Background()
	in := make(chan int)
	close(in)

	out := orDone(ctx, in)
	select {
	case _, ok := <-out:
		if ok {
			t.Fatal("expected channel to be closed")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for channel close")
	}
}
