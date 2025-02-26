package multi

import (
	"context"

	"github.com/aptpod/iscp-go/internal/ch"
	"github.com/aptpod/iscp-go/transport"
)

type EventScheduler struct {
	Subscriber Subscriber
}

type EventSchedulerFunc func(ctx context.Context) <-chan transport.TransportID

func (f EventSchedulerFunc) Subscribe(ctx context.Context) <-chan transport.TransportID {
	return f(ctx)
}

type Subscriber interface {
	Subscribe(ctx context.Context) <-chan transport.TransportID
}

func (e *EventScheduler) loop(ctx context.Context) <-chan transport.TransportID {
	return ch.ReadOrDone(ctx, e.Subscriber.Subscribe(ctx))
}
