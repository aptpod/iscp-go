package multi

import (
	"context"
	"time"

	"github.com/aptpod/iscp-go/internal/ch"
	"github.com/aptpod/iscp-go/transport"
)

type PollingScheduler struct {
	Poller   Poller
	Interval time.Duration
}

type Poller interface {
	Get() transport.TransportID
}

type MultiTransportSetter interface {
	SetMultiTransport(*Transport)
}

func (p *PollingScheduler) loop(ctx context.Context) <-chan transport.TransportID {
	resCh := make(chan transport.TransportID)
	go func() {
		defer close(resCh)
		ticker := time.NewTicker(p.Interval)
		defer ticker.Stop()
		for range ch.ReadOrDone(ctx, ticker.C) {
			ch.WriteOrDone(ctx, p.Poller.Get(), resCh)
		}
	}()
	return resCh
}
