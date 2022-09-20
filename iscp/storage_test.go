package iscp_test

import (
	"context"
	"testing"

	. "github.com/aptpod/iscp-go/iscp"
	"github.com/aptpod/iscp-go/message"
	uuid "github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSentStorage(t *testing.T) {
	type args struct {
		streamID uuid.UUID
		seq      uint32
		dps      DataPointGroups
	}
	tests := []struct {
		name          string
		args          args
		wantRemaining uint32
		want          DataPointGroups
	}{
		{
			name: "success: single",
			args: args{
				streamID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
				seq:      1,
				dps: []*DataPointGroup{
					{DataID: &message.DataID{Name: "name", Type: "type"}, DataPoints: DataPoints{{ElapsedTime: 1, Payload: []byte{0, 1, 2, 3, 4}}}},
				},
			},
			wantRemaining: 1,
			want: []*DataPointGroup{
				{DataID: &message.DataID{Name: "name", Type: "type"}, DataPoints: DataPoints{{ElapsedTime: 1, Payload: []byte{0, 1, 2, 3, 4}}}},
			},
		},
		{
			name: "success: multi",
			args: args{
				streamID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
				seq:      1,
				dps: []*DataPointGroup{
					{DataID: &message.DataID{Name: "name", Type: "type"}, DataPoints: DataPoints{{ElapsedTime: 1, Payload: []byte{0, 1, 2, 3, 4}}}},
					{DataID: &message.DataID{Name: "name", Type: "type"}, DataPoints: DataPoints{{ElapsedTime: 2, Payload: []byte{0, 1, 2, 3, 4}}}},
					{DataID: &message.DataID{Name: "name", Type: "type"}, DataPoints: DataPoints{{ElapsedTime: 3, Payload: []byte{0, 1, 2, 3, 4}}}},
				},
			},
			wantRemaining: 1,
			want: []*DataPointGroup{
				{DataID: &message.DataID{Name: "name", Type: "type"}, DataPoints: DataPoints{{ElapsedTime: 1, Payload: []byte{0, 1, 2, 3, 4}}}},
				{DataID: &message.DataID{Name: "name", Type: "type"}, DataPoints: DataPoints{{ElapsedTime: 2, Payload: []byte{0, 1, 2, 3, 4}}}},
				{DataID: &message.DataID{Name: "name", Type: "type"}, DataPoints: DataPoints{{ElapsedTime: 3, Payload: []byte{0, 1, 2, 3, 4}}}},
			},
		},
	}
	for _, tt := range tests {
		testSentStorage := func(s SentStorage) func(t *testing.T) {
			return func(t *testing.T) {
				t.Helper()
				ctx := context.Background()
				err := s.Store(ctx, tt.args.streamID, tt.args.seq, tt.args.dps)
				require.NoError(t, err)

				gotRemaining, err := s.Remaining(ctx, tt.args.streamID)
				require.NoError(t, err)
				assert.Equal(t, tt.wantRemaining, gotRemaining)

				got, err := s.Remove(ctx, tt.args.streamID, tt.args.seq)
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)

				gotRemaining, err = s.Remaining(ctx, tt.args.streamID)
				require.NoError(t, err)
				assert.EqualValues(t, 0, gotRemaining)
			}
		}
		t.Run(tt.name, testSentStorage(NewInmemSentStorage()))
		for _, v := range tt.want {
			for _, vv := range v.DataPoints {
				vv.Payload = nil
			}
		}
		t.Run(tt.name+"noPayload", testSentStorage(NewInmemSentStorageNoPayload()))
	}
}

func TestSentStorage_Error(t *testing.T) {
	type args struct {
		streamID uuid.UUID
		seq      uint32
		dps      DataPointGroups
	}
	tests := []struct {
		name      string
		fixture   args
		args      args
		assertion assert.ErrorAssertionFunc
	}{
		{
			name: "no stream id",
			args: args{
				streamID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
				seq:      1,
				dps: []*DataPointGroup{
					{DataID: &message.DataID{Name: "name", Type: "type"}, DataPoints: DataPoints{{ElapsedTime: 1, Payload: []byte{0, 1, 2, 3, 4}}}},
				},
			},
			assertion: assert.Error,
		},
		{
			name: "no seq",
			fixture: args{
				streamID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
				seq:      1,
				dps: []*DataPointGroup{
					{DataID: &message.DataID{Name: "name", Type: "type"}, DataPoints: DataPoints{{ElapsedTime: 1, Payload: []byte{0, 1, 2, 3, 4}}}},
				},
			},
			args: args{
				streamID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
				seq:      2,
				dps: []*DataPointGroup{
					{DataID: &message.DataID{Name: "name", Type: "type"}, DataPoints: DataPoints{{ElapsedTime: 1, Payload: []byte{0, 1, 2, 3, 4}}}},
				},
			},
			assertion: assert.NoError,
		},
	}
	for _, tt := range tests {
		testSentStorage := func(s SentStorage) func(t *testing.T) {
			return func(t *testing.T) {
				t.Helper()
				ctx := context.Background()
				err := s.Store(ctx, tt.fixture.streamID, tt.fixture.seq, tt.fixture.dps)
				require.NoError(t, err)

				_, err = s.Remaining(ctx, tt.args.streamID)
				tt.assertion(t, err)

				_, err = s.Remove(ctx, tt.args.streamID, tt.args.seq)
				assert.Error(t, err)
			}
		}
		t.Run(tt.name, testSentStorage(NewInmemSentStorage()))
		t.Run(tt.name+"noPayload", testSentStorage(NewInmemSentStorageNoPayload()))
	}
}
