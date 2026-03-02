package multi

import (
	"context"

	"github.com/aptpod/iscp-go/v2/internal/ch"
	"github.com/aptpod/iscp-go/v2/transport"
	"github.com/aptpod/iscp-go/v2/transport/nic"
)

type NICEventListener interface {
	Subscribe() <-chan string
}

var (
	_ Subscriber       = (*NICEventSubscriber)(nil)
	_ NICEventListener = (*nic.Manager)(nil)
)

type NICEventSubscriber struct {
	NICManager         NICEventListener
	NICSubConnectionID map[string]transport.SubConnectionID
}

// Subscribe implements Subscriber.
func (n *NICEventSubscriber) Subscribe(ctx context.Context) <-chan transport.SubConnectionID {
	resCh := make(chan transport.SubConnectionID, 1)
	go func() {
		defer close(resCh)
		for nic := range ch.ReadOrDone(ctx, n.NICManager.Subscribe()) {
			ch.WriteOrDone(ctx, n.NICSubConnectionID[nic], resCh)
		}
	}()
	return resCh
}
