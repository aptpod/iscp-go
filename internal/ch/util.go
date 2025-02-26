package ch

import "context"

func WriteOrDone[T any](ctx context.Context, v T, c chan<- T) {
	select {
	case c <- v:
	case <-ctx.Done():
	}
}

func ReadOrDone[T any](ctx context.Context, c <-chan T) <-chan T {
	resCh := make(chan T)
	go func() {
		defer close(resCh)
		for {
			v, ok := ReadOrDoneOne(ctx, c)
			if !ok {
				return
			}
			WriteOrDone(ctx, v, resCh)
		}
	}()
	return resCh
}

func ReadOrDoneOne[T any](ctx context.Context, c <-chan T) (T, bool) {
	var t T
	select {
	case <-ctx.Done():
		return t, false
	case v, ok := <-c:
		if !ok {
			return t, false
		}
		return v, true
	}
}
