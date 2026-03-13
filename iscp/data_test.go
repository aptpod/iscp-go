package iscp_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	. "github.com/aptpod/iscp-go/iscp"
	"github.com/aptpod/iscp-go/message"
)

func TestDataPoints_toUpstreamDataPointGroups(t *testing.T) {
	type args struct{}
	tests := []struct {
		name       string
		dps        DataPointGroups
		revAliases map[message.DataID]uint32
		want       []*message.UpstreamDataPointGroup
		wantIDs    []*message.DataID
	}{
		{
			name: "success",
			dps: DataPointGroups{
				{
					DataID: &message.DataID{
						Name: "1",
						Type: "1",
					},
					DataPoints: DataPoints{
						{
							ElapsedTime: time.Second,
							Payload:     []byte{1},
						},
					},
				},
				{
					DataID: &message.DataID{
						Name: "1",
						Type: "1",
					},
					DataPoints: DataPoints{
						{
							ElapsedTime: time.Second * 2,
							Payload:     []byte{2},
						},
					},
				},
				{
					DataID: &message.DataID{
						Name: "2",
						Type: "2",
					},
					DataPoints: DataPoints{
						{
							ElapsedTime: time.Second,
							Payload:     []byte{1},
						},
					},
				},
				{
					DataID: &message.DataID{
						Name: "2",
						Type: "2",
					},
					DataPoints: DataPoints{
						{
							ElapsedTime: time.Second * 2,
							Payload:     []byte{2},
						},
					},
				},
			},
			revAliases: map[message.DataID]uint32{
				{Name: "1", Type: "1"}: 1,
			},
			want: []*message.UpstreamDataPointGroup{
				{
					DataIDOrAlias: message.DataIDAlias(1),
					DataPoints: []*message.DataPoint{
						{
							ElapsedTime: time.Second,
							Payload:     []byte{1},
						},
					},
				},
				{
					DataIDOrAlias: message.DataIDAlias(1),
					DataPoints: []*message.DataPoint{
						{
							ElapsedTime: time.Second * 2,
							Payload:     []byte{2},
						},
					},
				},
				{
					DataIDOrAlias: &message.DataID{
						Name: "2",
						Type: "2",
					},
					DataPoints: []*message.DataPoint{
						{
							ElapsedTime: time.Second,
							Payload:     []byte{1},
						},
					},
				},
				{
					DataIDOrAlias: &message.DataID{
						Name: "2",
						Type: "2",
					},
					DataPoints: []*message.DataPoint{
						{
							ElapsedTime: time.Second * 2,
							Payload:     []byte{2},
						},
					},
				},
			},
			wantIDs: []*message.DataID{
				{
					Name: "2",
					Type: "2",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotIDs := ToUpstreamDataPointGroups(&tt.dps, tt.revAliases)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantIDs, gotIDs)
		})
	}
}
