package multi

import (
	"context"

	"github.com/aptpod/iscp-go/internal/ch"
	"github.com/aptpod/iscp-go/transport"
	"github.com/aptpod/iscp-go/transport/nic"
)

type NICEventListener interface {
	Subscribe() <-chan string
}

var (
	_ Subscriber       = (*NICEventSubscriber)(nil)
	_ NICEventListener = (*nic.Manager)(nil)
)

type NICEventSubscriber struct {
	NICManager     NICEventListener
	NICTransportID map[string]transport.TransportID
}

// Subscribe implements Subscriber.
func (n *NICEventSubscriber) Subscribe(ctx context.Context) <-chan transport.TransportID {
	resCh := make(chan transport.TransportID, 1)
	go func() {
		defer close(resCh)
		for nic := range ch.ReadOrDone(ctx, n.NICManager.Subscribe()) {
			ch.WriteOrDone(ctx, n.NICTransportID[nic], resCh)
		}
	}()
	return resCh
}
