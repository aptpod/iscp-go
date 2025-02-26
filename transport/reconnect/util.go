package reconnect

import (
	"context"

	"github.com/aptpod/iscp-go/internal/ch"
)

func writeOrDone[T any](ctx context.Context, v T, c chan<- T) {
	ch.WriteOrDone(ctx, v, c)
}

func readOrDone[T any](ctx context.Context, c <-chan T) <-chan T {
	return ch.ReadOrDone(ctx, c)
}

func readOrDoneOne[T any](ctx context.Context, c <-chan T) (T, bool) {
	return ch.ReadOrDoneOne(ctx, c)
}
