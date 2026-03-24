package multi_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/aptpod/iscp-go/transport"
	. "github.com/aptpod/iscp-go/transport/multi"
	"github.com/aptpod/iscp-go/transport/reconnect"
)

func TestSelectAvailableTransportFunc(t *testing.T) {
	tests := []struct {
		name         string
		selectedID   transport.SubConnectionID
		transportIDs []transport.SubConnectionID
		statuses     map[transport.SubConnectionID]reconnect.Status
		want         transport.SubConnectionID
	}{
		{
			name:         "selected is connected",
			selectedID:   "t1",
			transportIDs: []transport.SubConnectionID{"t1", "t2"},
			statuses: map[transport.SubConnectionID]reconnect.Status{
				"t1": reconnect.StatusConnected,
				"t2": reconnect.StatusConnected,
			},
			want: "t1",
		},
		{
			name:         "selected not connected, fallback to other connected",
			selectedID:   "t1",
			transportIDs: []transport.SubConnectionID{"t1", "t2"},
			statuses: map[transport.SubConnectionID]reconnect.Status{
				"t1": reconnect.StatusReconnecting,
				"t2": reconnect.StatusConnected,
			},
			want: "t2",
		},
		{
			name:         "none connected, fallback to reconnecting",
			selectedID:   "t1",
			transportIDs: []transport.SubConnectionID{"t1", "t2"},
			statuses: map[transport.SubConnectionID]reconnect.Status{
				"t1": reconnect.StatusDisconnected,
				"t2": reconnect.StatusReconnecting,
			},
			want: "t2",
		},
		{
			name:         "none connected, fallback to connecting",
			selectedID:   "t1",
			transportIDs: []transport.SubConnectionID{"t1", "t2"},
			statuses: map[transport.SubConnectionID]reconnect.Status{
				"t1": reconnect.StatusDisconnected,
				"t2": reconnect.StatusConnecting,
			},
			want: "t2",
		},
		{
			name:         "all disconnected returns empty",
			selectedID:   "t1",
			transportIDs: []transport.SubConnectionID{"t1", "t2"},
			statuses: map[transport.SubConnectionID]reconnect.Status{
				"t1": reconnect.StatusDisconnected,
				"t2": reconnect.StatusDisconnected,
			},
			want: "",
		},
		{
			name:         "empty transport list returns empty",
			selectedID:   "t1",
			transportIDs: []transport.SubConnectionID{},
			statuses:     map[transport.SubConnectionID]reconnect.Status{},
			want:         "",
		},
		{
			name:         "selected not in list, fallback to connected",
			selectedID:   "t_unknown",
			transportIDs: []transport.SubConnectionID{"t1", "t2"},
			statuses: map[transport.SubConnectionID]reconnect.Status{
				"t1": reconnect.StatusConnected,
				"t2": reconnect.StatusReconnecting,
			},
			want: "t1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getStatus := func(id transport.SubConnectionID) reconnect.Status {
				if s, ok := tt.statuses[id]; ok {
					return s
				}
				return reconnect.StatusDisconnected
			}
			got := SelectAvailableTransportFunc(tt.selectedID, tt.transportIDs, getStatus)
			assert.Equal(t, tt.want, got)
		})
	}
}
