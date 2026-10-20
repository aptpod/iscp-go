package multi_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/aptpod/iscp-go/v2/transport"
	"github.com/aptpod/iscp-go/v2/transport/multi"
)

type mockNICEventSubscriber struct {
	ch chan string
}

func newMockNICEventSubscriber() *mockNICEventSubscriber {
	return &mockNICEventSubscriber{
		ch: make(chan string, 1),
	}
}

func (m *mockNICEventSubscriber) Subscribe() <-chan string {
	return m.ch
}

func TestNICEventScheduler(t *testing.T) {
	tests := []struct {
		name     string
		nicID    string
		wantTID  transport.SubConnectionID
		canceled bool
	}{
		{
			name:    "Success: NIC event is correctly converted to SubConnectionID",
			nicID:   "eth0",
			wantTID: transport.SubConnectionID("transport1"),
		},
		{
			name:     "Cancel: Context is canceled",
			nicID:    "eth0",
			wantTID:  transport.SubConnectionID("transport1"),
			canceled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Prepare the mock
			mockNIC := newMockNICEventSubscriber()
			scheduler := &multi.NICEventSubscriber{
				NICManager: mockNIC,
				NICSubConnectionID: map[string]transport.SubConnectionID{
					tt.nicID: tt.wantTID,
				},
			}

			// Prepare the context
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			// Start Subscribe
			ch := scheduler.Subscribe(ctx)

			if tt.canceled {
				// Test the cancel case
				cancel()
				time.Sleep(10 * time.Millisecond) // wait for the goroutine to finish
				select {
				case _, ok := <-ch:
					assert.False(t, ok, "channel should be closed")
				default:
					t.Error("channel is not closed")
				}
				return
			}

			// Send NIC events
			mockNIC.ch <- tt.nicID

			// Verify the results
			select {
			case got := <-ch:
				assert.Equal(t, tt.wantTID, got)
			case <-time.After(100 * time.Millisecond):
				t.Error("timeout: failed to receive SubConnectionID")
			}
		})
	}
}

func TestNICEventScheduler_UnknownNIC(t *testing.T) {
	mockNIC := newMockNICEventSubscriber()
	scheduler := &multi.NICEventSubscriber{
		NICManager:         mockNIC,
		NICSubConnectionID: map[string]transport.SubConnectionID{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := scheduler.Subscribe(ctx)

	// Send an unknown NIC event
	mockNIC.ch <- "unknown_nic"

	// Verify that it is skipped when there is no corresponding SubConnectionID and nothing is sent to the channel
	select {
	case tid := <-ch:
		t.Errorf("unexpected SubConnectionID received for unknown NIC: %q", tid)
	case <-time.After(100 * time.Millisecond):
		// As expected: the unknown NIC is skipped
	}
}
