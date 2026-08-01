package iscp_test

import (
	"context"
	stdlog "log"
	"math"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	uuid "github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/aptpod/iscp-go/v2/errors"
	"github.com/aptpod/iscp-go/v2/iscp"
	. "github.com/aptpod/iscp-go/v2/iscp"
	"github.com/aptpod/iscp-go/v2/log"
	"github.com/aptpod/iscp-go/v2/message"
	"github.com/aptpod/iscp-go/v2/transport"
)

type CaptureHooker struct {
	afterReceivedAckCh chan struct {
		StreamID     uuid.UUID
		Sequence     uint32
		ResultCode   message.ResultCode
		ResultString string
	}

	beforeSendDataPointsCh chan struct {
		StreamID   uuid.UUID
		Sequence   uint32
		DataPoints DataPointGroups
	}
}

func NewCaptureHooker() *CaptureHooker {
	return &CaptureHooker{
		afterReceivedAckCh: make(chan struct {
			StreamID     uuid.UUID
			Sequence     uint32
			ResultCode   message.ResultCode
			ResultString string
		}, 1024),
		beforeSendDataPointsCh: make(chan struct {
			StreamID   uuid.UUID
			Sequence   uint32
			DataPoints DataPointGroups
		}, 1024),
	}
}

func (c *CaptureHooker) HookAfter(streamID uuid.UUID, ack UpstreamChunkResult) {
	select {
	case c.afterReceivedAckCh <- struct {
		StreamID     uuid.UUID
		Sequence     uint32
		ResultCode   message.ResultCode
		ResultString string
	}{
		StreamID:     streamID,
		Sequence:     ack.SequenceNumber,
		ResultCode:   ack.ResultCode,
		ResultString: ack.ResultString,
	}:
	default:
	}
}

func (c *CaptureHooker) HookBefore(streamID uuid.UUID, sequenceNumber uint32, dataPoints DataPointGroups) {
	select {
	case c.beforeSendDataPointsCh <- struct {
		StreamID   uuid.UUID
		Sequence   uint32
		DataPoints DataPointGroups
	}{
		StreamID:   streamID,
		Sequence:   sequenceNumber,
		DataPoints: dataPoints,
	}:
	default:
	}
}

func pDuration(d time.Duration) *time.Duration {
	return &d
}

func TestUpstream_SendDataPointWithAck(t *testing.T) {
	tests := []struct {
		name string
		qos  message.QoS
	}{
		{
			name: "success reliable",
			qos:  message.QoSReliable,
		},
		{
			name: "success unreliable",
			qos:  message.QoSUnreliable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer goleak.VerifyNone(t)
			d := newDialer(transport.NegotiationParams{})
			RegisterDialer(TransportTest, func() transport.Dialer { return d })
			done := make(chan struct{}, 0)
			defer func() {
				<-done
			}()
			go func() {
				defer close(done)
				mockConnectRequest(t, d.srv)
				upstreamOpenReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamOpenRequest)
				assert.Equal(t, &message.UpstreamOpenRequest{
					RequestID:       upstreamOpenReq.RequestID,
					SessionID:       "session_id",
					AckInterval:     time.Millisecond,
					ExpiryInterval:  time.Second * 10,
					DataIDs:         []*message.DataID{},
					QoS:             tt.qos,
					ExtensionFields: &message.UpstreamOpenRequestExtensionFields{},
				}, upstreamOpenReq)

				mustWrite(t, d.srv, &message.UpstreamOpenResponse{
					RequestID:             upstreamOpenReq.RequestID,
					AssignedStreamID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
					AssignedStreamIDAlias: 1,
					ResultCode:            message.ResultCodeSucceeded,
					ResultString:          "OK",
					DataIDAliases:         map[uint32]*message.DataID{},
				})

				chunk := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamChunk)
				assert.Equal(t, &message.UpstreamChunk{
					StreamIDAlias: 1,
					DataIDs: []*message.DataID{
						{
							Name: "name",
							Type: "type",
						},
					},
					StreamChunk: &message.StreamChunk{
						SequenceNumber: 1,
						DataPointGroups: []*message.DataPointGroup{
							{
								DataIDOrAlias: &message.DataID{
									Name: "name",
									Type: "type",
								},
								DataPoints: []*message.DataPoint{
									{
										ElapsedTime: time.Second,
										Payload:     []byte{1, 2, 3, 4},
									},
								},
							},
						},
					},
				}, chunk)
				mustWrite(t, d.srv, &message.UpstreamChunkAck{
					StreamIDAlias: 1,
					Results: []*message.UpstreamChunkResult{
						{
							SequenceNumber: chunk.StreamChunk.SequenceNumber,
							ResultCode:     message.ResultCodeSucceeded,
							ResultString:   "OK",
						},
					},
					DataIDAliases:   map[uint32]*message.DataID{},
					ExtensionFields: &message.UpstreamChunkAckExtensionFields{},
				})

				closeReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamCloseRequest)
				assert.Equal(t, &message.UpstreamCloseRequest{
					RequestID:           closeReq.RequestID,
					StreamID:            uuid.MustParse("11111111-1111-1111-1111-111111111111"),
					TotalDataPoints:     1,
					FinalSequenceNumber: 1,
					ExtensionFields:     &message.UpstreamCloseRequestExtensionFields{},
				}, closeReq)
				mustWrite(t, d.srv, &message.UpstreamCloseResponse{
					RequestID:    closeReq.RequestID,
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "OK",
				})
				assert.Equal(t, &message.Disconnect{
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "NormalClosure",
				}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}))
			}()

			ctx := context.Background()
			conn, err := Connect("dummy", TransportTest,
				iscp.WithConnNodeID("11111111-1111-1111-1111-111111111111"),
			)
			require.NoError(t, err)
			defer conn.Close(ctx)

			hooker := NewCaptureHooker()
			up, err := conn.OpenUpstream(ctx,
				"session_id",
				WithUpstreamAckInterval(time.Millisecond),
				WithUpstreamFlushPolicyIntervalOnly(time.Millisecond),
				WithUpstreamQoS(tt.qos),
				WithUpstreamReceiveAckHooker(hooker),
			)
			require.NoError(t, err)
			defer up.Close(ctx)

			stub := &DataPointGroup{
				DataID: &message.DataID{
					Name: "name",
					Type: "type",
				},
				DataPoints: DataPoints{
					{
						ElapsedTime: time.Second,
						Payload:     []byte{1, 2, 3, 4},
					},
				},
			}
			err = up.WriteDataPoints(ctx, stub.DataID, stub.DataPoints...)
			require.NoError(t, err)

			ack := <-hooker.afterReceivedAckCh
			assert.Equal(t, message.ResultCodeSucceeded, ack.ResultCode)
			assert.Equal(t, "OK", ack.ResultString)
		})
	}
}

func TestUpstream_SendDataPointWithAck_Close(t *testing.T) {
	tests := []struct {
		name string
		qos  message.QoS
	}{
		{
			name: "success reliable",
			qos:  message.QoSReliable,
		},
		{
			name: "success unreliable",
			qos:  message.QoSUnreliable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer goleak.VerifyNone(t)
			d := newDialer(transport.NegotiationParams{})
			RegisterDialer(TransportTest, func() transport.Dialer { return d })
			done := make(chan struct{}, 0)
			defer func() {
				<-done
			}()
			go func() {
				defer close(done)
				mockConnectRequest(t, d.srv)
				upstreamOpenReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamOpenRequest)
				assert.Equal(t, &message.UpstreamOpenRequest{
					RequestID:       upstreamOpenReq.RequestID,
					SessionID:       "session_id",
					AckInterval:     time.Millisecond,
					ExpiryInterval:  time.Second * 10,
					DataIDs:         []*message.DataID{},
					QoS:             tt.qos,
					ExtensionFields: &message.UpstreamOpenRequestExtensionFields{},
				}, upstreamOpenReq)

				mustWrite(t, d.srv, &message.UpstreamOpenResponse{
					RequestID:             upstreamOpenReq.RequestID,
					AssignedStreamID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
					AssignedStreamIDAlias: 1,
					ResultCode:            message.ResultCodeSucceeded,
					ResultString:          "OK",
					DataIDAliases:         map[uint32]*message.DataID{},
				})

				chunk := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamChunk)
				assert.Equal(t, &message.UpstreamChunk{
					StreamIDAlias: 1,
					DataIDs: []*message.DataID{
						{
							Name: "name",
							Type: "type",
						},
					},
					StreamChunk: &message.StreamChunk{
						SequenceNumber: 1,
						DataPointGroups: []*message.DataPointGroup{
							{
								DataIDOrAlias: &message.DataID{
									Name: "name",
									Type: "type",
								},
								DataPoints: []*message.DataPoint{
									{
										ElapsedTime: time.Second,
										Payload:     []byte{1, 2, 3, 4},
									},
								},
							},
						},
					},
				}, chunk)
				mustWrite(t, d.srv, &message.UpstreamChunkAck{
					StreamIDAlias: 1,
					Results: []*message.UpstreamChunkResult{
						{
							SequenceNumber: chunk.StreamChunk.SequenceNumber,
							ResultCode:     message.ResultCodeSucceeded,
							ResultString:   "OK",
						},
					},
					DataIDAliases:   map[uint32]*message.DataID{},
					ExtensionFields: &message.UpstreamChunkAckExtensionFields{},
				})

				closeReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamCloseRequest)
				assert.Equal(t, &message.UpstreamCloseRequest{
					RequestID:           closeReq.RequestID,
					StreamID:            uuid.MustParse("11111111-1111-1111-1111-111111111111"),
					TotalDataPoints:     1,
					FinalSequenceNumber: 1,
					ExtensionFields:     &message.UpstreamCloseRequestExtensionFields{},
				}, closeReq)
				mustWrite(t, d.srv, &message.UpstreamCloseResponse{
					RequestID:    closeReq.RequestID,
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "OK",
				})
				assert.Equal(t, &message.Disconnect{
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "NormalClosure",
				}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}))
			}()

			ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
			defer cancel()

			conn, err := Connect("dummy", TransportTest,
				iscp.WithConnNodeID("11111111-1111-1111-1111-111111111111"),
			)
			require.NoError(t, err)
			defer conn.Close(ctx)

			hooker := NewCaptureHooker()
			up, err := conn.OpenUpstream(ctx,
				"session_id",
				WithUpstreamAckInterval(time.Millisecond),
				WithUpstreamFlushPolicyIntervalOnly(time.Millisecond),
				WithUpstreamQoS(tt.qos),
				WithUpstreamReceiveAckHooker(hooker),
			)
			require.NoError(t, err)

			stub := &DataPointGroup{
				DataID: &message.DataID{
					Name: "name",
					Type: "type",
				},
				DataPoints: DataPoints{
					{
						ElapsedTime: time.Second,
						Payload:     []byte{1, 2, 3, 4},
					},
				},
			}
			err = up.WriteDataPoints(ctx, stub.DataID, stub.DataPoints...)
			require.NoError(t, err)
			up.Close(ctx)
			ack := <-hooker.afterReceivedAckCh
			assert.Equal(t, message.ResultCodeSucceeded, ack.ResultCode)
			assert.Equal(t, "OK", ack.ResultString)
		})
	}
}

func TestUpstream_SendDataPointWithoutAck(t *testing.T) {
	tests := []struct {
		name string
		qos  message.QoS
	}{
		{
			name: "success reliable",
			qos:  message.QoSReliable,
		},
		{
			name: "success unreliable",
			qos:  message.QoSUnreliable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer goleak.VerifyNone(t)
			d := newDialer(transport.NegotiationParams{})
			RegisterDialer(TransportTest, func() transport.Dialer { return d })
			done := make(chan struct{}, 0)
			defer func() {
				<-done
			}()
			go func() {
				defer close(done)
				mockConnectRequest(t, d.srv)
				upstreamOpenReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamOpenRequest)
				assert.Equal(t, &message.UpstreamOpenRequest{
					RequestID:       upstreamOpenReq.RequestID,
					SessionID:       "session_id",
					AckInterval:     time.Millisecond,
					ExpiryInterval:  time.Second * 10,
					DataIDs:         []*message.DataID{},
					QoS:             tt.qos,
					ExtensionFields: &message.UpstreamOpenRequestExtensionFields{},
				}, upstreamOpenReq)

				mustWrite(t, d.srv, &message.UpstreamOpenResponse{
					RequestID:             upstreamOpenReq.RequestID,
					AssignedStreamID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
					AssignedStreamIDAlias: 1,
					ResultCode:            message.ResultCodeSucceeded,
					ResultString:          "OK",
					DataIDAliases:         map[uint32]*message.DataID{},
				})

				chunk := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamChunk)
				assert.Equal(t, &message.UpstreamChunk{
					StreamIDAlias: 1,
					DataIDs: []*message.DataID{
						{
							Name: "name",
							Type: "type",
						},
					},
					StreamChunk: &message.StreamChunk{
						SequenceNumber: 1,
						DataPointGroups: []*message.DataPointGroup{
							{
								DataIDOrAlias: &message.DataID{
									Name: "name",
									Type: "type",
								},
								DataPoints: []*message.DataPoint{
									{
										ElapsedTime: time.Millisecond * 100,
										Payload:     []byte{1, 2, 3, 4},
									},
								},
							},
						},
					},
				}, chunk)
				mustWrite(t, d.srv, &message.UpstreamChunkAck{
					StreamIDAlias: 1,
					Results: []*message.UpstreamChunkResult{
						{
							SequenceNumber: chunk.StreamChunk.SequenceNumber,
							ResultCode:     message.ResultCodeSucceeded,
							ResultString:   "OK",
						},
					},
					DataIDAliases:   map[uint32]*message.DataID{},
					ExtensionFields: &message.UpstreamChunkAckExtensionFields{},
				})

				closeReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamCloseRequest)
				assert.Equal(t, &message.UpstreamCloseRequest{
					RequestID:           closeReq.RequestID,
					StreamID:            uuid.MustParse("11111111-1111-1111-1111-111111111111"),
					TotalDataPoints:     1,
					FinalSequenceNumber: 1,
					ExtensionFields:     &message.UpstreamCloseRequestExtensionFields{},
				}, closeReq)
				mustWrite(t, d.srv, &message.UpstreamCloseResponse{
					RequestID:    closeReq.RequestID,
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "OK",
				})
				assert.Equal(t, &message.Disconnect{
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "NormalClosure",
				}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}))
			}()

			ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
			defer cancel()

			conn, err := Connect("dummy", TransportTest,
				iscp.WithConnNodeID("11111111-1111-1111-1111-111111111111"),
			)
			require.NoError(t, err)
			defer conn.Close(ctx)

			up, err := conn.OpenUpstream(ctx,
				"session_id",
				WithUpstreamCloseTimeout(time.Second),
				WithUpstreamAckInterval(time.Millisecond),
				WithUpstreamFlushPolicyIntervalOnly(time.Millisecond),
				WithUpstreamQoS(tt.qos),
			)
			require.NoError(t, err)

			stub := &DataPointGroup{
				DataID: &message.DataID{
					Name: "name",
					Type: "type",
				},
				DataPoints: DataPoints{
					{
						ElapsedTime: time.Millisecond * 100,
						Payload:     []byte{1, 2, 3, 4},
					},
				},
			}
			err = up.WriteDataPoints(ctx, stub.DataID, stub.DataPoints...)
			require.NoError(t, err)
			// wait first flushing
			time.Sleep(time.Millisecond * 100)

			assert.NoError(t, up.Close(ctx))
			assert.True(t, up.IsReceivedLastSentAck())
		})
	}
}

func TestUpstream_SendDataPointOverSizeFlush(t *testing.T) {
	tests := []struct {
		name string
		qos  message.QoS
	}{
		{
			name: "success reliable",
			qos:  message.QoSReliable,
		},
		{
			name: "success unreliable",
			qos:  message.QoSUnreliable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer goleak.VerifyNone(t)
			d := newDialer(transport.NegotiationParams{})
			RegisterDialer(TransportTest, func() transport.Dialer { return d })
			done := make(chan struct{}, 0)
			defer func() {
				<-done
			}()
			go func() {
				defer close(done)
				mockConnectRequest(t, d.srv)
				upstreamOpenReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamOpenRequest)
				assert.Equal(t, &message.UpstreamOpenRequest{
					RequestID:       upstreamOpenReq.RequestID,
					SessionID:       "session_id",
					AckInterval:     0,
					ExpiryInterval:  time.Second * 10,
					DataIDs:         []*message.DataID{},
					QoS:             tt.qos,
					ExtensionFields: &message.UpstreamOpenRequestExtensionFields{},
				}, upstreamOpenReq)

				mustWrite(t, d.srv, &message.UpstreamOpenResponse{
					RequestID:             upstreamOpenReq.RequestID,
					AssignedStreamID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
					AssignedStreamIDAlias: 1,
					ResultCode:            message.ResultCodeSucceeded,
					ResultString:          "OK",
					DataIDAliases:         map[uint32]*message.DataID{},
				})

				chunk := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamChunk)
				assert.Equal(t, &message.UpstreamChunk{
					StreamIDAlias: 1,
					DataIDs: []*message.DataID{
						{
							Name: "name",
							Type: "type",
						},
					},
					StreamChunk: &message.StreamChunk{
						SequenceNumber: 1,
						DataPointGroups: []*message.DataPointGroup{
							{
								DataIDOrAlias: &message.DataID{
									Name: "name",
									Type: "type",
								},
								DataPoints: []*message.DataPoint{
									{
										ElapsedTime: time.Millisecond * 100,
										Payload:     []byte{1, 2, 3, 4},
									},
								},
							},
						},
					},
				}, chunk)
				mustWrite(t, d.srv, &message.UpstreamChunkAck{
					StreamIDAlias: 1,
					Results: []*message.UpstreamChunkResult{
						{
							SequenceNumber: chunk.StreamChunk.SequenceNumber,
							ResultCode:     message.ResultCodeSucceeded,
							ResultString:   "OK",
						},
					},
					DataIDAliases:   map[uint32]*message.DataID{},
					ExtensionFields: &message.UpstreamChunkAckExtensionFields{},
				})
				closeRequest := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamCloseRequest)
				assert.Equal(t, &message.UpstreamCloseRequest{
					RequestID:           closeRequest.RequestID,
					StreamID:            uuid.MustParse("11111111-1111-1111-1111-111111111111"),
					TotalDataPoints:     1,
					FinalSequenceNumber: 1,
					ExtensionFields:     &message.UpstreamCloseRequestExtensionFields{},
				}, closeRequest)
				mustWrite(t, d.srv, &message.UpstreamCloseResponse{
					RequestID:    closeRequest.RequestID,
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "OK",
				})
				assert.Equal(t, &message.Disconnect{
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "NormalClosure",
				}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}))
			}()

			ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
			defer cancel()

			conn, err := Connect("dummy", TransportTest,
				iscp.WithConnNodeID("11111111-1111-1111-1111-111111111111"),
			)
			require.NoError(t, err)
			defer conn.Close(ctx)

			hooker := NewCaptureHooker()

			up, err := conn.OpenUpstream(ctx,
				"session_id",
				WithUpstreamQoS(tt.qos),
				WithUpstreamCloseTimeout(time.Second),
				WithUpstreamAckInterval(0),
				WithUpstreamFlushPolicyIntervalOrBufferSize(time.Second*10, 1),
				WithUpstreamReceiveAckHooker(hooker),
			)
			require.NoError(t, err)
			defer up.Close(ctx)

			stub := &DataPointGroup{
				DataID: &message.DataID{
					Name: "name",
					Type: "type",
				},
				DataPoints: DataPoints{
					{
						ElapsedTime: time.Millisecond * 100,
						Payload:     []byte{1, 2, 3, 4},
					},
				},
			}
			err = up.WriteDataPoints(ctx, stub.DataID, stub.DataPoints...)

			require.NoError(t, err)
			ack := <-hooker.afterReceivedAckCh
			assert.Equal(t, message.ResultCodeSucceeded, ack.ResultCode)
		})
	}
}

func TestUpstream_SendDataPointFlushExplicitly(t *testing.T) {
	tests := []struct {
		name string
		qos  message.QoS
	}{
		{
			name: "success reliable",
			qos:  message.QoSReliable,
		},
		{
			name: "success unreliable",
			qos:  message.QoSUnreliable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer goleak.VerifyNone(t)
			d := newDialer(transport.NegotiationParams{})
			RegisterDialer(TransportTest, func() transport.Dialer { return d })
			done := make(chan struct{}, 0)
			defer func() {
				<-done
			}()
			go func() {
				defer close(done)
				mockConnectRequest(t, d.srv)
				upstreamOpenReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamOpenRequest)
				assert.Equal(t, &message.UpstreamOpenRequest{
					RequestID:       upstreamOpenReq.RequestID,
					SessionID:       "session_id",
					AckInterval:     0,
					ExpiryInterval:  time.Second * 10,
					DataIDs:         []*message.DataID{},
					QoS:             tt.qos,
					ExtensionFields: &message.UpstreamOpenRequestExtensionFields{},
				}, upstreamOpenReq)

				mustWrite(t, d.srv, &message.UpstreamOpenResponse{
					RequestID:             upstreamOpenReq.RequestID,
					AssignedStreamID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
					AssignedStreamIDAlias: 1,
					ResultCode:            message.ResultCodeSucceeded,
					ResultString:          "OK",
					DataIDAliases:         map[uint32]*message.DataID{},
				})

				chunk := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamChunk)
				assert.Equal(t, &message.UpstreamChunk{
					StreamIDAlias: 1,
					DataIDs: []*message.DataID{
						{
							Name: "name",
							Type: "type",
						},
					},
					StreamChunk: &message.StreamChunk{
						SequenceNumber: 1,
						DataPointGroups: []*message.DataPointGroup{
							{
								DataIDOrAlias: &message.DataID{
									Name: "name",
									Type: "type",
								},
								DataPoints: []*message.DataPoint{
									{
										ElapsedTime: time.Millisecond * 100,
										Payload:     []byte{1, 2, 3, 4},
									},
								},
							},
						},
					},
				}, chunk)
				mustWrite(t, d.srv, &message.UpstreamChunkAck{
					StreamIDAlias: 1,
					Results: []*message.UpstreamChunkResult{
						{
							SequenceNumber: chunk.StreamChunk.SequenceNumber,
							ResultCode:     message.ResultCodeSucceeded,
							ResultString:   "OK",
						},
					},
					DataIDAliases:   map[uint32]*message.DataID{},
					ExtensionFields: &message.UpstreamChunkAckExtensionFields{},
				})
				closeRequest := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamCloseRequest)
				assert.Equal(t, &message.UpstreamCloseRequest{
					RequestID:           closeRequest.RequestID,
					StreamID:            uuid.MustParse("11111111-1111-1111-1111-111111111111"),
					TotalDataPoints:     1,
					FinalSequenceNumber: 1,
					ExtensionFields:     &message.UpstreamCloseRequestExtensionFields{},
				}, closeRequest)
				mustWrite(t, d.srv, &message.UpstreamCloseResponse{
					RequestID:    closeRequest.RequestID,
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "OK",
				})
				assert.Equal(t, &message.Disconnect{
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "NormalClosure",
				}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}))
			}()

			ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
			defer cancel()

			conn, err := Connect("dummy", TransportTest,
				iscp.WithConnNodeID("11111111-1111-1111-1111-111111111111"),
			)
			require.NoError(t, err)
			defer conn.Close(ctx)

			hooker := NewCaptureHooker()

			up, err := conn.OpenUpstream(ctx,
				"session_id",
				WithUpstreamQoS(tt.qos),
				WithUpstreamCloseTimeout(time.Second),
				WithUpstreamAckInterval(0),
				WithUpstreamFlushPolicyNone(),
				WithUpstreamReceiveAckHooker(hooker),
			)
			require.NoError(t, err)
			defer up.Close(ctx)

			stub := &DataPointGroup{
				DataID: &message.DataID{
					Name: "name",
					Type: "type",
				},
				DataPoints: DataPoints{
					{
						ElapsedTime: time.Millisecond * 100,
						Payload:     []byte{1, 2, 3, 4},
					},
				},
			}
			err = up.WriteDataPoints(ctx, stub.DataID, stub.DataPoints...)
			require.NoError(t, err)
			err = up.Flush(ctx)
			require.NoError(t, err)
			ack := <-hooker.afterReceivedAckCh
			assert.Equal(t, message.ResultCodeSucceeded, ack.ResultCode)
		})
	}
}

func TestUpstream_SendDataPointNoBuffer(t *testing.T) {
	tests := []struct {
		name string
		qos  message.QoS
	}{
		{
			name: "success reliable",
			qos:  message.QoSReliable,
		},
		{
			name: "success unreliable",
			qos:  message.QoSUnreliable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer goleak.VerifyNone(t)
			d := newDialer(transport.NegotiationParams{})
			RegisterDialer(TransportTest, func() transport.Dialer { return d })
			done := make(chan struct{}, 0)
			defer func() {
				<-done
			}()
			go func() {
				defer close(done)
				mockConnectRequest(t, d.srv)
				upstreamOpenReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamOpenRequest)
				assert.Equal(t, &message.UpstreamOpenRequest{
					RequestID:       upstreamOpenReq.RequestID,
					SessionID:       "session_id",
					AckInterval:     0,
					ExpiryInterval:  time.Second * 10,
					DataIDs:         []*message.DataID{},
					QoS:             tt.qos,
					ExtensionFields: &message.UpstreamOpenRequestExtensionFields{},
				}, upstreamOpenReq)

				mustWrite(t, d.srv, &message.UpstreamOpenResponse{
					RequestID:             upstreamOpenReq.RequestID,
					AssignedStreamID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
					AssignedStreamIDAlias: 1,
					ResultCode:            message.ResultCodeSucceeded,
					ResultString:          "OK",
					DataIDAliases:         map[uint32]*message.DataID{},
				})

				chunk := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamChunk)
				assert.Equal(t, &message.UpstreamChunk{
					StreamIDAlias: 1,
					DataIDs: []*message.DataID{
						{
							Name: "name",
							Type: "type",
						},
					},
					StreamChunk: &message.StreamChunk{
						SequenceNumber: 1,
						DataPointGroups: []*message.DataPointGroup{
							{
								DataIDOrAlias: &message.DataID{
									Name: "name",
									Type: "type",
								},
								DataPoints: []*message.DataPoint{
									{
										ElapsedTime: time.Millisecond * 100,
										Payload:     []byte{1, 2, 3, 4},
									},
								},
							},
						},
					},
				}, chunk)
				mustWrite(t, d.srv, &message.UpstreamChunkAck{
					StreamIDAlias: 1,
					Results: []*message.UpstreamChunkResult{
						{
							SequenceNumber: chunk.StreamChunk.SequenceNumber,
							ResultCode:     message.ResultCodeSucceeded,
							ResultString:   "OK",
						},
					},
					DataIDAliases:   map[uint32]*message.DataID{},
					ExtensionFields: &message.UpstreamChunkAckExtensionFields{},
				})
				closeRequest := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamCloseRequest)
				assert.Equal(t, &message.UpstreamCloseRequest{
					RequestID:           closeRequest.RequestID,
					StreamID:            uuid.MustParse("11111111-1111-1111-1111-111111111111"),
					TotalDataPoints:     1,
					FinalSequenceNumber: 1,
					ExtensionFields:     &message.UpstreamCloseRequestExtensionFields{},
				}, closeRequest)
				mustWrite(t, d.srv, &message.UpstreamCloseResponse{
					RequestID:    closeRequest.RequestID,
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "OK",
				})
				assert.Equal(t, &message.Disconnect{
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "NormalClosure",
				}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}))
			}()

			ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
			defer cancel()

			conn, err := Connect("dummy", TransportTest,
				iscp.WithConnNodeID("11111111-1111-1111-1111-111111111111"),
			)
			require.NoError(t, err)
			defer conn.Close(ctx)

			hooker := NewCaptureHooker()
			up, err := conn.OpenUpstream(ctx,
				"session_id",
				WithUpstreamQoS(tt.qos),
				WithUpstreamCloseTimeout(time.Second),
				WithUpstreamAckInterval(0),
				WithUpstreamFlushPolicyImmediately(),
				WithUpstreamReceiveAckHooker(hooker),
			)
			require.NoError(t, err)
			defer up.Close(ctx)

			stub := &DataPointGroup{
				DataID: &message.DataID{
					Name: "name",
					Type: "type",
				},
				DataPoints: DataPoints{
					{
						ElapsedTime: time.Millisecond * 100,
						Payload:     []byte{1, 2, 3, 4},
					},
				},
			}
			err = up.WriteDataPoints(ctx, stub.DataID, stub.DataPoints...)

			require.NoError(t, err)
			ack := <-hooker.afterReceivedAckCh
			assert.Equal(t, message.ResultCodeSucceeded, ack.ResultCode)
		})
	}
}

func TestUpstream_SendDataPointBulkAck(t *testing.T) {
	tests := []struct {
		name string
		qos  message.QoS
	}{
		{
			name: "success reliable",
			qos:  message.QoSReliable,
		},
		{
			name: "success unreliable",
			qos:  message.QoSUnreliable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer goleak.VerifyNone(t)
			d := newDialer(transport.NegotiationParams{})
			RegisterDialer(TransportTest, func() transport.Dialer { return d })
			done := make(chan struct{}, 0)
			defer func() {
				<-done
			}()
			go func() {
				defer close(done)
				mockConnectRequest(t, d.srv)
				upstreamOpenReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamOpenRequest)
				assert.Equal(t, &message.UpstreamOpenRequest{
					RequestID:       upstreamOpenReq.RequestID,
					SessionID:       "session_id",
					AckInterval:     time.Millisecond * 10,
					ExpiryInterval:  time.Second * 10,
					DataIDs:         []*message.DataID{},
					QoS:             tt.qos,
					ExtensionFields: &message.UpstreamOpenRequestExtensionFields{},
				}, upstreamOpenReq)

				mustWrite(t, d.srv, &message.UpstreamOpenResponse{
					RequestID:             upstreamOpenReq.RequestID,
					AssignedStreamID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
					AssignedStreamIDAlias: 1,
					ResultCode:            message.ResultCodeSucceeded,
					ResultString:          "OK",
					DataIDAliases:         map[uint32]*message.DataID{},
				})

				chunk := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamChunk)
				assert.Equal(t, &message.UpstreamChunk{
					StreamIDAlias: 1,
					DataIDs: []*message.DataID{
						{
							Name: "name",
							Type: "type",
						},
					},
					StreamChunk: &message.StreamChunk{
						SequenceNumber: 1,
						DataPointGroups: []*message.DataPointGroup{
							{
								DataIDOrAlias: &message.DataID{
									Name: "name",
									Type: "type",
								},
								DataPoints: []*message.DataPoint{
									{ElapsedTime: time.Millisecond * 0, Payload: []byte{1, 2, 3, 4}},
									{ElapsedTime: time.Millisecond * 1, Payload: []byte{1, 2, 3, 4}},
									{ElapsedTime: time.Millisecond * 2, Payload: []byte{1, 2, 3, 4}},
									{ElapsedTime: time.Millisecond * 3, Payload: []byte{1, 2, 3, 4}},
									{ElapsedTime: time.Millisecond * 4, Payload: []byte{1, 2, 3, 4}},
									{ElapsedTime: time.Millisecond * 5, Payload: []byte{1, 2, 3, 4}},
									{ElapsedTime: time.Millisecond * 6, Payload: []byte{1, 2, 3, 4}},
									{ElapsedTime: time.Millisecond * 7, Payload: []byte{1, 2, 3, 4}},
									{ElapsedTime: time.Millisecond * 8, Payload: []byte{1, 2, 3, 4}},
									{ElapsedTime: time.Millisecond * 9, Payload: []byte{1, 2, 3, 4}},
								},
							},
						},
					},
				}, chunk)
				mustWrite(t, d.srv, &message.UpstreamChunkAck{
					StreamIDAlias: 1,
					Results: []*message.UpstreamChunkResult{
						{
							SequenceNumber: chunk.StreamChunk.SequenceNumber,
							ResultCode:     message.ResultCodeSucceeded,
							ResultString:   "OK",
						},
					},
					DataIDAliases:   map[uint32]*message.DataID{},
					ExtensionFields: &message.UpstreamChunkAckExtensionFields{},
				})
				closeRequest := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamCloseRequest)
				assert.Equal(t, &message.UpstreamCloseRequest{
					RequestID:           closeRequest.RequestID,
					StreamID:            uuid.MustParse("11111111-1111-1111-1111-111111111111"),
					TotalDataPoints:     10,
					FinalSequenceNumber: 1,
					ExtensionFields:     &message.UpstreamCloseRequestExtensionFields{},
				}, closeRequest)
				mustWrite(t, d.srv, &message.UpstreamCloseResponse{
					RequestID:    closeRequest.RequestID,
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "OK",
				})
				assert.Equal(t, &message.Disconnect{
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "NormalClosure",
				}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}))
			}()

			ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
			defer cancel()

			conn, err := Connect("dummy", TransportTest,
				iscp.WithConnNodeID("11111111-1111-1111-1111-111111111111"),
			)
			require.NoError(t, err)
			defer conn.Close(ctx)

			hooker := NewCaptureHooker()
			up, err := conn.OpenUpstream(ctx,
				"session_id",
				WithUpstreamQoS(tt.qos),
				WithUpstreamCloseTimeout(time.Second),
				WithUpstreamAckInterval(time.Millisecond*10),
				WithUpstreamFlushPolicyIntervalOrBufferSize(time.Millisecond*10, 10000),
				WithUpstreamReceiveAckHooker(hooker),
			)
			require.NoError(t, err)
			defer up.Close(ctx)

			for i := 0; i < 10; i++ {
				err = up.WriteDataPoints(ctx, &message.DataID{
					Name: "name",
					Type: "type",
				}, &message.DataPoint{
					ElapsedTime: time.Millisecond * time.Duration(i),
					Payload:     []byte{1, 2, 3, 4},
				})
				require.NoError(t, err)
			}

			ack := <-hooker.afterReceivedAckCh
			assert.Equal(t, message.ResultCodeSucceeded, ack.ResultCode)
		})
		t.Run(tt.name+"data points", func(t *testing.T) {
			defer goleak.VerifyNone(t)
			d := newDialer(transport.NegotiationParams{})
			RegisterDialer(TransportTest, func() transport.Dialer { return d })
			done := make(chan struct{}, 0)
			defer func() {
				<-done
			}()
			go func() {
				defer close(done)
				mockConnectRequest(t, d.srv)
				upstreamOpenReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamOpenRequest)
				assert.Equal(t, &message.UpstreamOpenRequest{
					RequestID:       upstreamOpenReq.RequestID,
					SessionID:       "session_id",
					AckInterval:     time.Millisecond * 10,
					ExpiryInterval:  time.Second * 10,
					DataIDs:         []*message.DataID{},
					QoS:             tt.qos,
					ExtensionFields: &message.UpstreamOpenRequestExtensionFields{},
				}, upstreamOpenReq)

				mustWrite(t, d.srv, &message.UpstreamOpenResponse{
					RequestID:             upstreamOpenReq.RequestID,
					AssignedStreamID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
					AssignedStreamIDAlias: 1,
					ResultCode:            message.ResultCodeSucceeded,
					ResultString:          "OK",
					DataIDAliases:         map[uint32]*message.DataID{},
				})

				chunk := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamChunk)
				assert.Equal(t, &message.UpstreamChunk{
					StreamIDAlias: 1,
					DataIDs: []*message.DataID{
						{
							Name: "name",
							Type: "type",
						},
					},
					StreamChunk: &message.StreamChunk{
						SequenceNumber: 1,
						DataPointGroups: []*message.DataPointGroup{
							{
								DataIDOrAlias: &message.DataID{
									Name: "name",
									Type: "type",
								},
								DataPoints: []*message.DataPoint{
									{ElapsedTime: time.Millisecond * 0, Payload: []byte{1, 2, 3, 4}},
									{ElapsedTime: time.Millisecond * 1, Payload: []byte{1, 2, 3, 4}},
									{ElapsedTime: time.Millisecond * 2, Payload: []byte{1, 2, 3, 4}},
									{ElapsedTime: time.Millisecond * 3, Payload: []byte{1, 2, 3, 4}},
									{ElapsedTime: time.Millisecond * 4, Payload: []byte{1, 2, 3, 4}},
									{ElapsedTime: time.Millisecond * 5, Payload: []byte{1, 2, 3, 4}},
									{ElapsedTime: time.Millisecond * 6, Payload: []byte{1, 2, 3, 4}},
									{ElapsedTime: time.Millisecond * 7, Payload: []byte{1, 2, 3, 4}},
									{ElapsedTime: time.Millisecond * 8, Payload: []byte{1, 2, 3, 4}},
									{ElapsedTime: time.Millisecond * 9, Payload: []byte{1, 2, 3, 4}},
								},
							},
						},
					},
				}, chunk)
				mustWrite(t, d.srv, &message.UpstreamChunkAck{
					StreamIDAlias: 1,
					Results: []*message.UpstreamChunkResult{
						{
							SequenceNumber: chunk.StreamChunk.SequenceNumber,
							ResultCode:     message.ResultCodeSucceeded,
							ResultString:   "OK",
						},
					},
					DataIDAliases:   map[uint32]*message.DataID{},
					ExtensionFields: &message.UpstreamChunkAckExtensionFields{},
				})

				closeRequest := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamCloseRequest)
				assert.Equal(t, &message.UpstreamCloseRequest{
					RequestID:           closeRequest.RequestID,
					StreamID:            uuid.MustParse("11111111-1111-1111-1111-111111111111"),
					TotalDataPoints:     10,
					FinalSequenceNumber: 1,
					ExtensionFields:     &message.UpstreamCloseRequestExtensionFields{},
				}, closeRequest)
				mustWrite(t, d.srv, &message.UpstreamCloseResponse{
					RequestID:    closeRequest.RequestID,
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "OK",
				})
				assert.Equal(t, &message.Disconnect{
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "NormalClosure",
				}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}))
			}()

			ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
			defer cancel()

			conn, err := Connect("dummy", TransportTest,
				iscp.WithConnNodeID("11111111-1111-1111-1111-111111111111"),
			)
			require.NoError(t, err)
			defer conn.Close(ctx)

			hooker := NewCaptureHooker()
			up, err := conn.OpenUpstream(ctx,
				"session_id",
				WithUpstreamQoS(tt.qos),
				WithUpstreamCloseTimeout(time.Second),
				WithUpstreamAckInterval(time.Millisecond*10),
				WithUpstreamFlushPolicyIntervalOrBufferSize(time.Millisecond*10, 10000),
				WithUpstreamReceiveAckHooker(hooker),
			)
			require.NoError(t, err)
			defer up.Close(ctx)

			dps := &DataPointGroup{
				DataID: &message.DataID{
					Name: "name",
					Type: "type",
				},
			}
			for i := 0; i < 10; i++ {
				dps.DataPoints = append(dps.DataPoints, &message.DataPoint{
					ElapsedTime: time.Millisecond * time.Duration(i),
					Payload:     []byte{1, 2, 3, 4},
				})
			}
			err = up.WriteDataPoints(ctx, dps.DataID, dps.DataPoints...)
			require.NoError(t, err)
			ack := <-hooker.afterReceivedAckCh
			assert.Equal(t, message.ResultCodeSucceeded, ack.ResultCode)
		})
	}
}

func TestUpstream_ClientConnClose(t *testing.T) {
	tests := []struct {
		name string
		qos  message.QoS
	}{
		{
			name: "success reliable",
			qos:  message.QoSReliable,
		},
		{
			name: "success unreliable",
			qos:  message.QoSUnreliable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer goleak.VerifyNone(t)
			d := newDialer(transport.NegotiationParams{})
			RegisterDialer(TransportTest, func() transport.Dialer { return d })
			done := make(chan struct{}, 0)
			defer func() {
				<-done
			}()
			go func() {
				defer close(done)
				mockConnectRequest(t, d.srv)
				upstreamOpenReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamOpenRequest)
				assert.Equal(t, &message.UpstreamOpenRequest{
					RequestID:       upstreamOpenReq.RequestID,
					SessionID:       "session_id",
					AckInterval:     10 * time.Millisecond,
					ExpiryInterval:  time.Second * 10,
					DataIDs:         []*message.DataID{},
					QoS:             tt.qos,
					ExtensionFields: &message.UpstreamOpenRequestExtensionFields{},
				}, upstreamOpenReq)

				mustWrite(t, d.srv, &message.UpstreamOpenResponse{
					RequestID:             upstreamOpenReq.RequestID,
					AssignedStreamID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
					AssignedStreamIDAlias: 1,
					ResultCode:            message.ResultCodeSucceeded,
					ResultString:          "OK",
					DataIDAliases:         map[uint32]*message.DataID{},
				})

				assert.Equal(t, &message.Disconnect{
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "NormalClosure",
				}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}))
			}()

			ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
			defer cancel()

			conn, err := Connect("dummy", TransportTest,
				iscp.WithConnNodeID("11111111-1111-1111-1111-111111111111"),
			)
			require.NoError(t, err)

			hooker := NewCaptureHooker()
			up, err := conn.OpenUpstream(ctx,
				"session_id",
				WithUpstreamCloseTimeout(time.Second),
				WithUpstreamAckInterval(time.Millisecond*10),
				WithUpstreamFlushPolicyIntervalOrBufferSize(time.Millisecond*10, 10000),
				WithUpstreamReceiveAckHooker(hooker),
				WithUpstreamQoS(tt.qos),
			)
			require.NoError(t, err)
			conn.Close(ctx)
			// NONE: connのクローズとアップストリームのクローズは非同期で行われるため、少し待たないと稀にWriteDataPointsが成功してしまう
			// TODO: connのクローズは同期的に状態遷移させる
			<-time.After(time.Microsecond * 100)

			assert.Error(t, up.WriteDataPoints(ctx, &message.DataID{}, &message.DataPoint{}))
			assert.Error(t, up.Flush(ctx))
			assert.NoError(t, up.Close(ctx))
		})
	}
}

func Test_sequenceNumberGenerator_Next(t *testing.T) {
	s := &SequenceNumberGenerator{
		Current: 0,
	}
	assert.Equal(t, uint32(0), s.CurrentValue())
	assert.Equal(t, uint32(1), s.Next())
	assert.Equal(t, uint32(1), s.CurrentValue())

	assert.Equal(t, uint32(2), s.Next())
	assert.Equal(t, uint32(2), s.CurrentValue())

	// start 0
	s = &SequenceNumberGenerator{
		Current: math.MaxUint32,
	}
	assert.Equal(t, uint32(0), s.Next())
}

func TestUpstream_Resume_Unreliable(t *testing.T) {
	nodeID := "11111111-1111-1111-1111-111111111111"
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	dialers := []*dialer{newDialer(transport.NegotiationParams{}), newDialer(transport.NegotiationParams{})}
	registerTestTransport(t, dialers)
	go func() {
		d := dialers[0]
		mockConnectRequest(t, d.srv)
		msg, ok := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamOpenRequest)
		require.True(t, ok)
		mustWrite(t, d.srv, &message.UpstreamOpenResponse{
			RequestID:        msg.RequestID,
			AssignedStreamID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			ResultCode:       message.ResultCodeSucceeded,
			ResultString:     "OK",
			ExtensionFields:  &message.UpstreamOpenResponseExtensionFields{},
		})
		t.Log("Server:OpenUpstream")
	}()

	done := make(chan struct{}, 0)
	defer func() {
		<-done
	}()
	go func() {
		defer close(done)
		d := dialers[1]
		mockConnectRequest(t, d.srv)
		t.Log("Server:Reconnected")
		msg := mustRead(t, d.srv, &message.Ping{}, &message.Pong{})
		req, ok := msg.(*message.UpstreamResumeRequest)
		require.True(t, ok, "%T", msg)
		assert.Equal(t, &message.UpstreamResumeRequest{
			RequestID: req.RequestID,
			StreamID:  uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		}, msg)
		mustWrite(t, d.srv, &message.UpstreamResumeResponse{
			RequestID:             req.RequestID,
			AssignedStreamIDAlias: uint32(1),
			ResultCode:            message.ResultCodeSucceeded,
			ResultString:          "OK",
			ExtensionFields:       &message.UpstreamResumeResponseExtensionFields{},
		})

		for {
			msg := mustRead(t, d.srv, &message.Ping{}, &message.Pong{})
			switch m := msg.(type) {
			case *message.UpstreamChunk:
				mustWrite(t, d.srv, &message.UpstreamChunkAck{
					StreamIDAlias: uint32(1),
					Results: []*message.UpstreamChunkResult{
						{
							SequenceNumber:  m.StreamChunk.SequenceNumber,
							ResultCode:      message.ResultCodeSucceeded,
							ResultString:    "OK",
							ExtensionFields: &message.UpstreamChunkResultExtensionFields{},
						},
					},
					DataIDAliases:   map[uint32]*message.DataID{},
					ExtensionFields: &message.UpstreamChunkAckExtensionFields{},
				})
				continue
			case *message.UpstreamCloseRequest:
				// Unreliableモードでは抜けが許容されるため、値の範囲チェックのみ
				assert.Equal(t, uuid.MustParse("11111111-1111-1111-1111-111111111111"), m.StreamID)
				assert.GreaterOrEqual(t, m.TotalDataPoints, uint64(1000), "should have sent at least 1000 data points")
				assert.GreaterOrEqual(t, m.FinalSequenceNumber, uint32(1000), "final sequence should be at least 1000")
				assert.NotNil(t, m.ExtensionFields)
				assert.False(t, m.ExtensionFields.CloseSession)
				mustWrite(t, d.srv, &message.UpstreamCloseResponse{
					RequestID:    m.RequestID,
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "OK",
				})
			}
			break
		}

		assert.Equal(t, &message.Disconnect{
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "NormalClosure",
		}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}))
	}()
	ctx := context.Background()
	conn, err := Connect("dummy", TransportTest,
		iscp.WithConnNodeID(nodeID),
		iscp.WithConnLogger(log.NewStdWith(stdlog.New(os.Stderr, "SERVER:", stdlog.LstdFlags))),
	)
	require.NoError(t, err)
	defer conn.Close(ctx)

	gotCh := make(chan UpstreamChunkResult, 1024)

	gotSeqNumsCond := sync.NewCond(&sync.Mutex{})
	gotSeqNums := make([]uint32, 0)
	closedEvCh := make(chan *UpstreamClosedEvent, 1)
	resumedEvCh := make(chan *UpstreamResumedEvent, 1)
	up, err := conn.OpenUpstream(ctx,
		"session_id",
		WithUpstreamCloseTimeout(time.Millisecond),
		WithUpstreamAckInterval(time.Millisecond*10),
		WithUpstreamFlushPolicyImmediately(),
		WithUpstreamExpiryInterval(time.Second*10),
		WithUpstreamReceiveAckHooker(ReceiveAckHookerFunc(func(streamID uuid.UUID, ack UpstreamChunkResult) {
			gotCh <- ack
		})),
		WithUpstreamSendDataPointsHooker(SendDataPointsHookerFunc(func(streamID uuid.UUID, chunk UpstreamChunk) {
			gotSeqNumsCond.L.Lock()
			gotSeqNums = append(gotSeqNums, chunk.SequenceNumber)
			gotSeqNumsCond.Signal()
			gotSeqNumsCond.L.Unlock()
		})),
		WithUpstreamClosedEventHandler(UpstreamClosedEventHandlerFunc(func(ev *iscp.UpstreamClosedEvent) {
			closedEvCh <- ev
		})),
		WithUpstreamResumedEventHandler(UpstreamResumedEventHandlerFunc(func(ev *iscp.UpstreamResumedEvent) {
			resumedEvCh <- ev
		})),
	)
	require.NoError(t, err)
	defer up.Close(ctx)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	dataPointCount := 1000
	// close 1st transport
	dialers[0].Close()
	writeDataPoints(t, ctx, up, dataPointCount, 0)

	gotSeqNumsCond.L.Lock()
	for len(gotSeqNums) != dataPointCount {
		gotSeqNumsCond.Wait()
	}
	gotSeqNumsCond.L.Unlock()

	// Unreliableモードでは抜けが許容されるため、何らかのシーケンス番号が発行されていることのみ確認
	assert.NotEmpty(t, gotSeqNums, "should have sent some data points")
	assert.GreaterOrEqual(t, len(gotSeqNums), dataPointCount, "should have sent at least %d data points", dataPointCount)

	assert.GreaterOrEqual(t, up.State().LastIssuedSequenceNumber, uint32(len(gotSeqNums)))
	assert.GreaterOrEqual(t, up.State().TotalDataPoints, uint64(dataPointCount))

	go writeDataPoints(t, ctx, up, 1000, time.Millisecond*10)

	got := <-gotCh
	got.SequenceNumber = 0
	assert.Equal(t, UpstreamChunkResult{
		ResultCode:   message.ResultCodeSucceeded,
		ResultString: "OK",
	}, got)
	up.Close(ctx)
	<-resumedEvCh

	gotClosedEvent := <-closedEvCh
	assert.Equal(t, up.State(), &gotClosedEvent.State)
}

func TestUpstream_Resume_Failure(t *testing.T) {
	defer goleak.VerifyNone(t)
	ds := []*dialer{newDialer(transport.NegotiationParams{}), newDialer(transport.NegotiationParams{})}
	registerTestTransport(t, ds)
	done := make(chan struct{}, 0)
	defer func() {
		<-done
	}()
	go func() {
		d := ds[0]
		mockConnectRequest(t, d.srv)
		msg, ok := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamOpenRequest)
		require.True(t, ok)
		mustWrite(t, d.srv, &message.UpstreamOpenResponse{
			RequestID:        msg.RequestID,
			AssignedStreamID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			ResultCode:       message.ResultCodeSucceeded,
			ResultString:     "OK",
			ExtensionFields:  &message.UpstreamOpenResponseExtensionFields{},
		})
		t.Log("Server:OpenUpstream")
	}()

	go func() {
		defer close(done)
		d := ds[1]
		mockConnectRequest(t, d.srv)
		t.Log("Server:Reconnected")
		msg := mustRead(t, d.srv, &message.Ping{}, &message.Pong{})
		req, ok := msg.(*message.UpstreamResumeRequest)
		require.True(t, ok, "%T", msg)
		assert.Equal(t, &message.UpstreamResumeRequest{
			RequestID: req.RequestID,
			StreamID:  uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		}, msg)
		mustWrite(t, d.srv, &message.UpstreamResumeResponse{
			RequestID:             req.RequestID,
			AssignedStreamIDAlias: uint32(1),
			ResultCode:            message.ResultCodeStreamNotFound,
			ResultString:          "Not Found Stream",
			ExtensionFields:       &message.UpstreamResumeResponseExtensionFields{},
		})

		m := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamCloseRequest)
		assert.Equal(t, &message.UpstreamCloseRequest{
			RequestID: m.RequestID,
			StreamID:  uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			ExtensionFields: &message.UpstreamCloseRequestExtensionFields{
				CloseSession: false,
			},
		}, m)

		mustWrite(t, d.srv, &message.UpstreamCloseResponse{
			RequestID:    m.RequestID,
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "OK",
		})

		assert.Equal(t, &message.Disconnect{
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "NormalClosure",
		}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}))
	}()
	ctx := context.Background()

	conn, err := Connect("dummy", TransportTest,
		iscp.WithConnNodeID("11111111-1111-1111-1111-111111111111"),
		iscp.WithConnLogger(log.NewStdWith(stdlog.New(os.Stderr, "CLIENT:", stdlog.LstdFlags))),
	)
	require.NoError(t, err)
	defer conn.Close(ctx)

	closedEvCh := make(chan *UpstreamClosedEvent, 1)
	up, err := conn.OpenUpstream(ctx,
		"session_id",
		WithUpstreamCloseTimeout(time.Second),
		WithUpstreamAckInterval(time.Millisecond*10),
		WithUpstreamFlushPolicyImmediately(),
		WithUpstreamExpiryInterval(time.Second),
		WithUpstreamClosedEventHandler(UpstreamClosedEventHandlerFunc(func(ev *iscp.UpstreamClosedEvent) {
			closedEvCh <- ev
		})),
	)
	require.NoError(t, err)
	defer up.Close(ctx)
	ds[0].Close()
	gotClosedEvent := <-closedEvCh
	assert.Equal(t, up.State(), &gotClosedEvent.State)
	var gotErr *errors.FailedMessageError
	require.ErrorAs(t, gotClosedEvent.Err, &gotErr)
	assert.Equal(t, message.ResultCodeStreamNotFound, gotErr.ResultCode)
}

func TestUpstream_SendDataPointFlush_Failure_Chunk_Creation(t *testing.T) {
	tests := []struct {
		name                          string
		qos                           message.QoS
		fixtureCurrentSequenceNumber  uint32
		fixtureCurrentTotalDataPoints uint64
		fixtureSendBufferDataPoints   int
		wantTotalDataPoints           uint64
		wantFinalSequenceNumber       uint32
	}{
		{
			name:                         "success reliable",
			qos:                          message.QoSReliable,
			fixtureCurrentSequenceNumber: math.MaxUint32,
			fixtureSendBufferDataPoints:  0,
			wantTotalDataPoints:          0,
			wantFinalSequenceNumber:      math.MaxUint32,
		},
		{
			name:                          "success unreliable",
			qos:                           message.QoSUnreliable,
			fixtureCurrentSequenceNumber:  0,
			fixtureCurrentTotalDataPoints: math.MaxUint64,
			fixtureSendBufferDataPoints:   1,
			wantTotalDataPoints:           math.MaxUint64,
			wantFinalSequenceNumber:       0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer goleak.VerifyNone(t)
			d := newDialer(transport.NegotiationParams{})
			RegisterDialer(TransportTest, func() transport.Dialer { return d })
			done := make(chan struct{}, 0)
			defer func() {
				<-done
			}()
			go func() {
				defer close(done)
				mockConnectRequest(t, d.srv)
				upstreamOpenReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamOpenRequest)
				assert.Equal(t, &message.UpstreamOpenRequest{
					RequestID:       upstreamOpenReq.RequestID,
					SessionID:       "session_id",
					AckInterval:     0,
					ExpiryInterval:  time.Second * 10,
					DataIDs:         []*message.DataID{},
					QoS:             tt.qos,
					ExtensionFields: &message.UpstreamOpenRequestExtensionFields{},
				}, upstreamOpenReq)

				mustWrite(t, d.srv, &message.UpstreamOpenResponse{
					RequestID:             upstreamOpenReq.RequestID,
					AssignedStreamID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
					AssignedStreamIDAlias: 1,
					ResultCode:            message.ResultCodeSucceeded,
					ResultString:          "OK",
					DataIDAliases:         map[uint32]*message.DataID{},
				})

				closeRequest := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamCloseRequest)
				assert.Equal(t, &message.UpstreamCloseRequest{
					RequestID:           closeRequest.RequestID,
					StreamID:            uuid.MustParse("11111111-1111-1111-1111-111111111111"),
					TotalDataPoints:     tt.wantTotalDataPoints,
					FinalSequenceNumber: tt.wantFinalSequenceNumber,
					ExtensionFields:     &message.UpstreamCloseRequestExtensionFields{},
				}, closeRequest)
				mustWrite(t, d.srv, &message.UpstreamCloseResponse{
					RequestID:    closeRequest.RequestID,
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "OK",
				})
				assert.Equal(t, &message.Disconnect{
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "NormalClosure",
				}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}))
			}()

			ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
			defer cancel()

			conn, err := Connect("dummy", TransportTest,
				iscp.WithConnNodeID("11111111-1111-1111-1111-111111111111"),
			)
			require.NoError(t, err)
			defer conn.Close(ctx)

			hooker := NewCaptureHooker()

			up, err := conn.OpenUpstream(ctx,
				"session_id",
				WithUpstreamQoS(tt.qos),
				WithUpstreamCloseTimeout(time.Second),
				WithUpstreamAckInterval(0),
				WithUpstreamFlushPolicyNone(),
				WithUpstreamReceiveAckHooker(hooker),
			)
			require.NoError(t, err)
			defer up.Close(ctx)
			up.SetSequenceNumber(t, tt.fixtureCurrentSequenceNumber)
			up.SetSendBufferDataPointsCount(t, tt.fixtureSendBufferDataPoints)
			up.SetCurrentTotalDataPoints(t, tt.fixtureCurrentTotalDataPoints)

			stub := &DataPointGroup{
				DataID: &message.DataID{
					Name: "name",
					Type: "type",
				},
				DataPoints: DataPoints{
					{
						ElapsedTime: time.Millisecond * 100,
						Payload:     []byte{1, 2, 3, 4},
					},
				},
			}
			err = up.WriteDataPoints(ctx, stub.DataID, stub.DataPoints...)
			require.NoError(t, err)
			err = up.Flush(ctx)
			require.Error(t, err)
		})
	}
}

func TestUpstream_Resume_Reliable(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	streamChunkCount := 100
	nodeID := "11111111-1111-1111-1111-111111111111"
	ds := []*dialer{newDialer(transport.NegotiationParams{}), newDialer(transport.NegotiationParams{})}
	registerTestTransport(t, ds)
	go func() {
		d := ds[0]
		mockConnectRequest(t, d.srv)
		msg, ok := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamOpenRequest)
		require.True(t, ok)
		mustWrite(t, d.srv, &message.UpstreamOpenResponse{
			RequestID:        msg.RequestID,
			AssignedStreamID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			ResultCode:       message.ResultCodeSucceeded,
			ResultString:     "OK",
			ExtensionFields:  &message.UpstreamOpenResponseExtensionFields{},
		})
		t.Log("Server:OpenUpstream")
	}()

	done := make(chan struct{}, 0)
	defer func() {
		<-done
	}()
	go func() {
		defer close(done)
		d := ds[1]
		mockConnectRequest(t, d.srv)
		t.Log("Server:Reconnected")
		msg := mustRead(t, d.srv, &message.Ping{}, &message.Pong{})
		req, ok := msg.(*message.UpstreamResumeRequest)
		require.True(t, ok, "%T", msg)
		assert.Equal(t, &message.UpstreamResumeRequest{
			RequestID: req.RequestID,
			StreamID:  uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		}, msg)
		mustWrite(t, d.srv, &message.UpstreamResumeResponse{
			RequestID:             req.RequestID,
			AssignedStreamIDAlias: uint32(1),
			ResultCode:            message.ResultCodeSucceeded,
			ResultString:          "OK",
			ExtensionFields:       &message.UpstreamResumeResponseExtensionFields{},
		})

		for {
			msg := mustRead(t, d.srv, &message.Ping{}, &message.Pong{})
			switch m := msg.(type) {
			case *message.UpstreamChunk:
				mustWrite(t, d.srv, &message.UpstreamChunkAck{
					StreamIDAlias: uint32(1),
					Results: []*message.UpstreamChunkResult{
						{
							SequenceNumber:  m.StreamChunk.SequenceNumber,
							ResultCode:      message.ResultCodeSucceeded,
							ResultString:    "OK",
							ExtensionFields: &message.UpstreamChunkResultExtensionFields{},
						},
					},
					DataIDAliases:   map[uint32]*message.DataID{},
					ExtensionFields: &message.UpstreamChunkAckExtensionFields{},
				})
				continue
			case *message.UpstreamCloseRequest:
				assert.Equal(t, &message.UpstreamCloseRequest{
					RequestID:           m.RequestID,
					StreamID:            uuid.MustParse("11111111-1111-1111-1111-111111111111"),
					TotalDataPoints:     uint64(streamChunkCount),
					FinalSequenceNumber: uint32(streamChunkCount),
					ExtensionFields: &message.UpstreamCloseRequestExtensionFields{
						CloseSession: false,
					},
				}, m)
				mustWrite(t, d.srv, &message.UpstreamCloseResponse{
					RequestID:    m.RequestID,
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "OK",
				})
			}
			break
		}

		assert.Equal(t, &message.Disconnect{
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "NormalClosure",
		}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}))
	}()
	conn, err := Connect("dummy", TransportTest,
		iscp.WithConnNodeID(nodeID),
		iscp.WithConnLogger(log.NewStdWith(stdlog.New(os.Stderr, "SERVER:", stdlog.LstdFlags))),
	)
	require.NoError(t, err)
	defer conn.Close(ctx)

	var capture hookerAndEventHandler
	up, err := conn.OpenUpstream(ctx,
		"session_id",
		WithUpstreamCloseTimeout(time.Millisecond),
		WithUpstreamAckInterval(time.Millisecond*10),
		WithUpstreamFlushPolicyImmediately(),
		WithUpstreamExpiryInterval(time.Second*10),
		WithUpstreamQoS(message.QoSReliable),
		WithUpstreamReceiveAckHooker(ReceiveAckHookerFunc(capture.ReceiveAck)),
		WithUpstreamSendDataPointsHooker(SendDataPointsHookerFunc(capture.SendDataPoints)),
		WithUpstreamClosedEventHandler(UpstreamClosedEventHandlerFunc(capture.UpstreamClosed)),
		WithUpstreamResumedEventHandler(UpstreamResumedEventHandlerFunc(capture.UpstreamResumed)),
	)
	require.NoError(t, err)
	defer up.Close(ctx)

	ctx, cancel = context.WithCancel(ctx)
	defer cancel()

	// close 1st transport
	ds[0].Close()
	wantDataPointGroups := writeDataPoints(t, ctx, up, streamChunkCount, time.Millisecond)

	assert.Eventually(t, func() bool {
		capture.Lock()
		defer capture.Unlock()
		return len(capture.upstreamChunks) >= streamChunkCount
	}, time.Second*10, time.Millisecond)

	for i, v := range capture.upstreamChunks {
		require.Equal(t, UpstreamChunk{
			SequenceNumber:  uint32(i + 1),
			DataPointGroups: DataPointGroups{wantDataPointGroups[i]},
		}, v)
	}

	assert.EqualValues(t, up.State().LastIssuedSequenceNumber, len(capture.upstreamChunks))
	assert.EqualValues(t, up.State().TotalDataPoints, streamChunkCount)

	assert.Eventually(t, func() bool {
		capture.Lock()
		defer capture.Unlock()
		return len(capture.upstreamResumedEvents) > 0
	}, time.Second*10, time.Millisecond)

	assert.Eventually(t, func() bool {
		capture.Lock()
		res := capture.upstreamChunkResults
		capture.Unlock()
		return len(removeDuplicateSequenceNumber(res)) == streamChunkCount
	}, time.Second*10, time.Millisecond)

	up.Close(ctx)
	assert.Eventually(t, func() bool {
		capture.Lock()
		defer capture.Unlock()
		return len(capture.upstreamClosedEvents) > 0
	}, time.Second*10, time.Millisecond)
	assert.Equal(t, up.State(), &capture.upstreamClosedEvents[0].State)
}

func registerTestTransport(t *testing.T, ds []*dialer) {
	t.Helper()
	var callCount int
	RegisterDialer(TransportTest, func() transport.Dialer {
		callCount++
		time.Sleep(time.Duration(callCount) * time.Millisecond)
		if len(ds) < callCount {
			return ds[len(ds)-1]
		}
		return ds[callCount-1]
	},
	)
}

func writeDataPoints(t *testing.T, ctx context.Context, up *Upstream, count int, interval time.Duration) DataPointGroups {
	t.Helper()
	var res DataPointGroups
	for i := 0; i < count; i++ {
		select {
		case <-ctx.Done():
			return res
		default:
		}
		dpg := &DataPointGroup{
			DataID: &message.DataID{
				Name: "name",
				Type: "string",
			},
			DataPoints: []*message.DataPoint{
				{
					ElapsedTime: time.Duration(i),
					Payload:     []byte("hello-world"),
				},
			},
		}
		res = append(res, dpg)
		up.WriteDataPoints(ctx, dpg.DataID, dpg.DataPoints...)
		time.Sleep(interval)
	}
	return res
}

func removeDuplicateSequenceNumber(src []UpstreamChunkResult) []UpstreamChunkResult {
	resMap := map[uint32]UpstreamChunkResult{}
	for _, v := range src {
		resMap[v.SequenceNumber] = v
	}
	var res []UpstreamChunkResult
	for _, v := range resMap {
		res = append(res, v)
	}
	sort.Slice(res, func(i, j int) bool { return res[i].SequenceNumber <= res[j].SequenceNumber })
	return res
}

type hookerAndEventHandler struct {
	sync.Mutex
	upstreamChunkResults  []UpstreamChunkResult
	upstreamChunks        []UpstreamChunk
	upstreamClosedEvents  []*UpstreamClosedEvent
	upstreamResumedEvents []*UpstreamResumedEvent
}

func (h *hookerAndEventHandler) ReceiveAck(streamID uuid.UUID, ack UpstreamChunkResult) {
	h.Lock()
	defer h.Unlock()

	h.upstreamChunkResults = append(h.upstreamChunkResults, ack)
	return
}

func (h *hookerAndEventHandler) SendDataPoints(streamID uuid.UUID, chunk UpstreamChunk) {
	h.Lock()
	defer h.Unlock()
	h.upstreamChunks = append(h.upstreamChunks, chunk)
	return
}

func (h *hookerAndEventHandler) UpstreamClosed(ev *iscp.UpstreamClosedEvent) {
	h.Lock()
	defer h.Unlock()
	h.upstreamClosedEvents = append(h.upstreamClosedEvents, ev)
	return
}

func (h *hookerAndEventHandler) UpstreamResumed(ev *iscp.UpstreamResumedEvent) {
	h.Lock()
	defer h.Unlock()
	h.upstreamResumedEvents = append(h.upstreamResumedEvents, ev)
	return
}

func TestUpstreamWriter_Write(t *testing.T) {
	tests := []struct {
		name string
		qos  message.QoS
	}{
		{
			name: "success reliable",
			qos:  message.QoSReliable,
		},
		{
			name: "success unreliable",
			qos:  message.QoSUnreliable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer goleak.VerifyNone(t)
			d := newDialer(transport.NegotiationParams{})
			RegisterDialer(TransportTest, func() transport.Dialer { return d })
			done := make(chan struct{})
			defer func() {
				<-done
			}()
			go func() {
				defer close(done)
				mockConnectRequest(t, d.srv)
				upstreamOpenReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamOpenRequest)
				mustWrite(t, d.srv, &message.UpstreamOpenResponse{
					RequestID:             upstreamOpenReq.RequestID,
					AssignedStreamID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
					AssignedStreamIDAlias: 1,
					ResultCode:            message.ResultCodeSucceeded,
					ResultString:          "OK",
					DataIDAliases:         map[uint32]*message.DataID{},
				})

				chunk := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamChunk)
				assert.Equal(t, &message.UpstreamChunk{
					StreamIDAlias: 1,
					DataIDs: []*message.DataID{
						{Name: "name", Type: "type"},
					},
					StreamChunk: &message.StreamChunk{
						SequenceNumber: 1,
						DataPointGroups: []*message.DataPointGroup{
							{
								DataIDOrAlias: &message.DataID{Name: "name", Type: "type"},
								DataPoints: []*message.DataPoint{
									{ElapsedTime: time.Second, Payload: []byte{1, 2, 3}},
								},
							},
						},
					},
				}, chunk)

				mustWrite(t, d.srv, &message.UpstreamChunkAck{
					StreamIDAlias: 1,
					Results: []*message.UpstreamChunkResult{
						{
							SequenceNumber: chunk.StreamChunk.SequenceNumber,
							ResultCode:     message.ResultCodeSucceeded,
							ResultString:   "OK",
						},
					},
					DataIDAliases:   map[uint32]*message.DataID{},
					ExtensionFields: &message.UpstreamChunkAckExtensionFields{},
				})

				closeReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamCloseRequest)
				mustWrite(t, d.srv, &message.UpstreamCloseResponse{
					RequestID:    closeReq.RequestID,
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "OK",
				})
				assert.Equal(t, &message.Disconnect{
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "NormalClosure",
				}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}))
			}()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			conn, err := Connect("dummy", TransportTest,
				iscp.WithConnNodeID("11111111-1111-1111-1111-111111111111"),
			)
			require.NoError(t, err)
			defer conn.Close(ctx)

			hooker := NewCaptureHooker()
			up, err := conn.OpenUpstream(ctx,
				"session_id",
				WithUpstreamAckInterval(time.Millisecond),
				WithUpstreamFlushPolicyIntervalOnly(time.Millisecond),
				WithUpstreamQoS(tt.qos),
				WithUpstreamReceiveAckHooker(hooker),
			)
			require.NoError(t, err)
			defer up.Close(ctx)

			w := up.NewWriter(&message.DataID{Name: "name", Type: "type"})
			err = w.Write(ctx, &message.DataPoint{ElapsedTime: time.Second, Payload: []byte{1, 2, 3}})
			require.NoError(t, err)

			ack := <-hooker.afterReceivedAckCh
			assert.Equal(t, message.ResultCodeSucceeded, ack.ResultCode)
			assert.Equal(t, "OK", ack.ResultString)
		})
	}
}

func TestUpstreamWriter_MultipleWriters(t *testing.T) {
	defer goleak.VerifyNone(t)

	d := newDialer(transport.NegotiationParams{})
	RegisterDialer(TransportTest, func() transport.Dialer { return d })
	done := make(chan struct{})
	defer func() {
		<-done
	}()

	go func() {
		defer close(done)
		mockConnectRequest(t, d.srv)
		upstreamOpenReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamOpenRequest)
		mustWrite(t, d.srv, &message.UpstreamOpenResponse{
			RequestID:             upstreamOpenReq.RequestID,
			AssignedStreamID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			AssignedStreamIDAlias: 1,
			ResultCode:            message.ResultCodeSucceeded,
			ResultString:          "OK",
			DataIDAliases:         map[uint32]*message.DataID{},
		})

		chunk := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamChunk)

		// 順序は不定なのでソートして比較する
		sort.Slice(chunk.DataIDs, func(i, j int) bool {
			return chunk.DataIDs[i].Name < chunk.DataIDs[j].Name
		})
		sort.Slice(chunk.StreamChunk.DataPointGroups, func(i, j int) bool {
			return chunk.StreamChunk.DataPointGroups[i].DataIDOrAlias.(*message.DataID).Name <
				chunk.StreamChunk.DataPointGroups[j].DataIDOrAlias.(*message.DataID).Name
		})

		assert.Equal(t, []*message.DataID{
			{Name: "alpha", Type: "type"},
			{Name: "beta", Type: "type"},
		}, chunk.DataIDs)
		assert.Equal(t, uint32(1), chunk.StreamIDAlias)
		assert.Equal(t, uint32(1), chunk.StreamChunk.SequenceNumber)
		assert.Equal(t, []*message.DataPointGroup{
			{
				DataIDOrAlias: &message.DataID{Name: "alpha", Type: "type"},
				DataPoints: []*message.DataPoint{
					{ElapsedTime: time.Second, Payload: []byte{0xAA}},
				},
			},
			{
				DataIDOrAlias: &message.DataID{Name: "beta", Type: "type"},
				DataPoints: []*message.DataPoint{
					{ElapsedTime: 2 * time.Second, Payload: []byte{0xBB}},
				},
			},
		}, chunk.StreamChunk.DataPointGroups)

		mustWrite(t, d.srv, &message.UpstreamChunkAck{
			StreamIDAlias: 1,
			Results: []*message.UpstreamChunkResult{
				{
					SequenceNumber: chunk.StreamChunk.SequenceNumber,
					ResultCode:     message.ResultCodeSucceeded,
					ResultString:   "OK",
				},
			},
			DataIDAliases:   map[uint32]*message.DataID{},
			ExtensionFields: &message.UpstreamChunkAckExtensionFields{},
		})

		closeReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamCloseRequest)
		assert.Equal(t, uint64(2), closeReq.TotalDataPoints)
		mustWrite(t, d.srv, &message.UpstreamCloseResponse{
			RequestID:    closeReq.RequestID,
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "OK",
		})
		assert.Equal(t, &message.Disconnect{
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "NormalClosure",
		}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}))
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := Connect("dummy", TransportTest,
		iscp.WithConnNodeID("11111111-1111-1111-1111-111111111111"),
	)
	require.NoError(t, err)
	defer conn.Close(ctx)

	hooker := NewCaptureHooker()
	up, err := conn.OpenUpstream(ctx,
		"session_id",
		WithUpstreamAckInterval(time.Millisecond),
		WithUpstreamFlushPolicyIntervalOnly(time.Millisecond),
		WithUpstreamQoS(message.QoSReliable),
		WithUpstreamReceiveAckHooker(hooker),
	)
	require.NoError(t, err)
	defer up.Close(ctx)

	wAlpha := up.NewWriter(&message.DataID{Name: "alpha", Type: "type"})
	wBeta := up.NewWriter(&message.DataID{Name: "beta", Type: "type"})

	err = wAlpha.Write(ctx, &message.DataPoint{ElapsedTime: time.Second, Payload: []byte{0xAA}})
	require.NoError(t, err)
	err = wBeta.Write(ctx, &message.DataPoint{ElapsedTime: 2 * time.Second, Payload: []byte{0xBB}})
	require.NoError(t, err)

	ack := <-hooker.afterReceivedAckCh
	assert.Equal(t, message.ResultCodeSucceeded, ack.ResultCode)
}

func TestUpstreamWriter_Close(t *testing.T) {
	defer goleak.VerifyNone(t)

	d := newDialer(transport.NegotiationParams{})
	RegisterDialer(TransportTest, func() transport.Dialer { return d })
	done := make(chan struct{})
	defer func() {
		<-done
	}()

	go func() {
		defer close(done)
		mockConnectRequest(t, d.srv)
		upstreamOpenReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamOpenRequest)
		mustWrite(t, d.srv, &message.UpstreamOpenResponse{
			RequestID:             upstreamOpenReq.RequestID,
			AssignedStreamID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			AssignedStreamIDAlias: 1,
			ResultCode:            message.ResultCodeSucceeded,
			ResultString:          "OK",
			DataIDAliases:         map[uint32]*message.DataID{},
		})
		closeReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamCloseRequest)
		mustWrite(t, d.srv, &message.UpstreamCloseResponse{
			RequestID:    closeReq.RequestID,
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "OK",
		})
		assert.Equal(t, &message.Disconnect{
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "NormalClosure",
		}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}))
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := Connect("dummy", TransportTest,
		iscp.WithConnNodeID("11111111-1111-1111-1111-111111111111"),
	)
	require.NoError(t, err)
	defer conn.Close(ctx)

	up, err := conn.OpenUpstream(ctx,
		"session_id",
		WithUpstreamAckInterval(time.Millisecond),
		WithUpstreamFlushPolicyIntervalOnly(time.Millisecond),
		WithUpstreamQoS(message.QoSReliable),
	)
	require.NoError(t, err)
	defer up.Close(ctx)

	w := up.NewWriter(&message.DataID{Name: "name", Type: "type"})

	// 1回目のCloseは成功する
	err = w.Close()
	require.NoError(t, err)

	// Closeした後のWriteはエラーを返す
	err = w.Write(ctx, &message.DataPoint{ElapsedTime: time.Second, Payload: []byte{1}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "writer is closed")

	// 2回目のCloseはエラーを返す
	err = w.Close()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "writer already closed")
}

func TestUpstream_WriteChunk(t *testing.T) {
	tests := []struct {
		name string
		qos  message.QoS
	}{
		{
			name: "success reliable",
			qos:  message.QoSReliable,
		},
		{
			name: "success unreliable",
			qos:  message.QoSUnreliable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer goleak.VerifyNone(t)
			d := newDialer(transport.NegotiationParams{})
			RegisterDialer(TransportTest, func() transport.Dialer { return d })
			done := make(chan struct{})
			defer func() {
				<-done
			}()
			go func() {
				defer close(done)
				mockConnectRequest(t, d.srv)
				upstreamOpenReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamOpenRequest)
				mustWrite(t, d.srv, &message.UpstreamOpenResponse{
					RequestID:             upstreamOpenReq.RequestID,
					AssignedStreamID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
					AssignedStreamIDAlias: 1,
					ResultCode:            message.ResultCodeSucceeded,
					ResultString:          "OK",
					DataIDAliases:         map[uint32]*message.DataID{},
				})

				chunk := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamChunk)
				assert.Equal(t, &message.UpstreamChunk{
					StreamIDAlias: 1,
					DataIDs: []*message.DataID{
						{Name: "name1", Type: "type1"},
						{Name: "name2", Type: "type2"},
					},
					StreamChunk: &message.StreamChunk{
						SequenceNumber: 1,
						DataPointGroups: []*message.DataPointGroup{
							{
								DataIDOrAlias: &message.DataID{Name: "name1", Type: "type1"},
								DataPoints: []*message.DataPoint{
									{ElapsedTime: time.Second, Payload: []byte{1, 2, 3}},
								},
							},
							{
								DataIDOrAlias: &message.DataID{Name: "name2", Type: "type2"},
								DataPoints: []*message.DataPoint{
									{ElapsedTime: time.Second * 2, Payload: []byte{4, 5, 6}},
								},
							},
						},
					},
				}, chunk)

				mustWrite(t, d.srv, &message.UpstreamChunkAck{
					StreamIDAlias: 1,
					Results: []*message.UpstreamChunkResult{
						{
							SequenceNumber: chunk.StreamChunk.SequenceNumber,
							ResultCode:     message.ResultCodeSucceeded,
							ResultString:   "OK",
						},
					},
					DataIDAliases:   map[uint32]*message.DataID{},
					ExtensionFields: &message.UpstreamChunkAckExtensionFields{},
				})

				closeReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamCloseRequest)
				assert.Equal(t, &message.UpstreamCloseRequest{
					RequestID:           closeReq.RequestID,
					StreamID:            uuid.MustParse("11111111-1111-1111-1111-111111111111"),
					TotalDataPoints:     2,
					FinalSequenceNumber: 1,
					ExtensionFields:     &message.UpstreamCloseRequestExtensionFields{},
				}, closeReq)
				mustWrite(t, d.srv, &message.UpstreamCloseResponse{
					RequestID:    closeReq.RequestID,
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "OK",
				})
				assert.Equal(t, &message.Disconnect{
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "NormalClosure",
				}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}))
			}()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			conn, err := Connect("dummy", TransportTest,
				iscp.WithConnNodeID("11111111-1111-1111-1111-111111111111"),
			)
			require.NoError(t, err)
			defer conn.Close(ctx)

			up, err := conn.OpenUpstream(ctx,
				"session_id",
				WithUpstreamAckInterval(time.Millisecond),
				WithUpstreamQoS(tt.qos),
			)
			require.NoError(t, err)
			defer up.Close(ctx)

			err = up.WriteChunk(ctx,
				&DataPointGroup{
					DataID:     &message.DataID{Name: "name1", Type: "type1"},
					DataPoints: DataPoints{{ElapsedTime: time.Second, Payload: []byte{1, 2, 3}}},
				},
				&DataPointGroup{
					DataID:     &message.DataID{Name: "name2", Type: "type2"},
					DataPoints: DataPoints{{ElapsedTime: time.Second * 2, Payload: []byte{4, 5, 6}}},
				},
			)
			require.NoError(t, err)
		})
	}
}

func TestUpstream_WriteChunkWithAck(t *testing.T) {
	defer goleak.VerifyNone(t)
	d := newDialer(transport.NegotiationParams{})
	RegisterDialer(TransportTest, func() transport.Dialer { return d })
	done := make(chan struct{})
	defer func() {
		<-done
	}()
	go func() {
		defer close(done)
		mockConnectRequest(t, d.srv)
		upstreamOpenReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamOpenRequest)
		mustWrite(t, d.srv, &message.UpstreamOpenResponse{
			RequestID:             upstreamOpenReq.RequestID,
			AssignedStreamID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			AssignedStreamIDAlias: 1,
			ResultCode:            message.ResultCodeSucceeded,
			ResultString:          "OK",
			DataIDAliases:         map[uint32]*message.DataID{},
		})

		chunk := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamChunk)
		mustWrite(t, d.srv, &message.UpstreamChunkAck{
			StreamIDAlias: 1,
			Results: []*message.UpstreamChunkResult{
				{
					SequenceNumber: chunk.StreamChunk.SequenceNumber,
					ResultCode:     message.ResultCodeSucceeded,
					ResultString:   "OK",
				},
			},
			DataIDAliases:   map[uint32]*message.DataID{},
			ExtensionFields: &message.UpstreamChunkAckExtensionFields{},
		})

		closeReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamCloseRequest)
		mustWrite(t, d.srv, &message.UpstreamCloseResponse{
			RequestID:    closeReq.RequestID,
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "OK",
		})
		assert.Equal(t, &message.Disconnect{
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "NormalClosure",
		}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}))
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := Connect("dummy", TransportTest,
		iscp.WithConnNodeID("11111111-1111-1111-1111-111111111111"),
	)
	require.NoError(t, err)
	defer conn.Close(ctx)

	hooker := NewCaptureHooker()
	up, err := conn.OpenUpstream(ctx,
		"session_id",
		WithUpstreamAckInterval(time.Millisecond),
		WithUpstreamQoS(message.QoSReliable),
		WithUpstreamReceiveAckHooker(hooker),
	)
	require.NoError(t, err)
	defer up.Close(ctx)

	err = up.WriteChunk(ctx,
		&DataPointGroup{
			DataID:     &message.DataID{Name: "name", Type: "type"},
			DataPoints: DataPoints{{ElapsedTime: time.Second, Payload: []byte{1, 2, 3}}},
		},
	)
	require.NoError(t, err)

	ack := <-hooker.afterReceivedAckCh
	assert.Equal(t, message.ResultCodeSucceeded, ack.ResultCode)
	assert.Equal(t, "OK", ack.ResultString)
	assert.Equal(t, uint32(1), ack.Sequence)
}

func TestUpstream_WriteChunkSequenceShared(t *testing.T) {
	defer goleak.VerifyNone(t)
	d := newDialer(transport.NegotiationParams{})
	RegisterDialer(TransportTest, func() transport.Dialer { return d })
	done := make(chan struct{})
	defer func() {
		<-done
	}()
	go func() {
		defer close(done)
		mockConnectRequest(t, d.srv)
		upstreamOpenReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamOpenRequest)
		mustWrite(t, d.srv, &message.UpstreamOpenResponse{
			RequestID:             upstreamOpenReq.RequestID,
			AssignedStreamID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			AssignedStreamIDAlias: 1,
			ResultCode:            message.ResultCodeSucceeded,
			ResultString:          "OK",
			DataIDAliases:         map[uint32]*message.DataID{},
		})

		// First chunk: from WriteDataPoints+Flush (sequence 1)
		chunk1 := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamChunk)
		assert.Equal(t, uint32(1), chunk1.StreamChunk.SequenceNumber)
		mustWrite(t, d.srv, &message.UpstreamChunkAck{
			StreamIDAlias: 1,
			Results: []*message.UpstreamChunkResult{
				{SequenceNumber: chunk1.StreamChunk.SequenceNumber, ResultCode: message.ResultCodeSucceeded, ResultString: "OK"},
			},
			DataIDAliases:   map[uint32]*message.DataID{},
			ExtensionFields: &message.UpstreamChunkAckExtensionFields{},
		})

		// Second chunk: from WriteChunk (sequence 2)
		chunk2 := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamChunk)
		assert.Equal(t, uint32(2), chunk2.StreamChunk.SequenceNumber)
		mustWrite(t, d.srv, &message.UpstreamChunkAck{
			StreamIDAlias: 1,
			Results: []*message.UpstreamChunkResult{
				{SequenceNumber: chunk2.StreamChunk.SequenceNumber, ResultCode: message.ResultCodeSucceeded, ResultString: "OK"},
			},
			DataIDAliases:   map[uint32]*message.DataID{},
			ExtensionFields: &message.UpstreamChunkAckExtensionFields{},
		})

		closeReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamCloseRequest)
		assert.Equal(t, uint32(2), closeReq.FinalSequenceNumber)
		mustWrite(t, d.srv, &message.UpstreamCloseResponse{
			RequestID:    closeReq.RequestID,
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "OK",
		})
		assert.Equal(t, &message.Disconnect{
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "NormalClosure",
		}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}))
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := Connect("dummy", TransportTest,
		iscp.WithConnNodeID("11111111-1111-1111-1111-111111111111"),
	)
	require.NoError(t, err)
	defer conn.Close(ctx)

	up, err := conn.OpenUpstream(ctx,
		"session_id",
		WithUpstreamAckInterval(time.Millisecond),
		WithUpstreamQoS(message.QoSReliable),
		WithUpstreamFlushPolicyImmediately(),
	)
	require.NoError(t, err)
	defer up.Close(ctx)

	// First: WriteDataPoints + Flush → sequence 1
	err = up.WriteDataPoints(ctx, &message.DataID{Name: "name", Type: "type"},
		&message.DataPoint{ElapsedTime: time.Second, Payload: []byte{1}},
	)
	require.NoError(t, err)
	err = up.Flush(ctx)
	require.NoError(t, err)

	// Second: WriteChunk → sequence 2
	err = up.WriteChunk(ctx,
		&DataPointGroup{
			DataID:     &message.DataID{Name: "name", Type: "type"},
			DataPoints: DataPoints{{ElapsedTime: time.Second * 2, Payload: []byte{2}}},
		},
	)
	require.NoError(t, err)

	assert.Equal(t, uint32(2), up.State().LastIssuedSequenceNumber)
}

func TestUpstream_WriteChunkEmpty(t *testing.T) {
	defer goleak.VerifyNone(t)
	d := newDialer(transport.NegotiationParams{})
	RegisterDialer(TransportTest, func() transport.Dialer { return d })
	done := make(chan struct{})
	defer func() {
		<-done
	}()
	go func() {
		defer close(done)
		mockConnectRequest(t, d.srv)
		upstreamOpenReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamOpenRequest)
		mustWrite(t, d.srv, &message.UpstreamOpenResponse{
			RequestID:             upstreamOpenReq.RequestID,
			AssignedStreamID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			AssignedStreamIDAlias: 1,
			ResultCode:            message.ResultCodeSucceeded,
			ResultString:          "OK",
			DataIDAliases:         map[uint32]*message.DataID{},
		})

		closeReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamCloseRequest)
		mustWrite(t, d.srv, &message.UpstreamCloseResponse{
			RequestID:    closeReq.RequestID,
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "OK",
		})
		assert.Equal(t, &message.Disconnect{
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "NormalClosure",
		}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}))
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := Connect("dummy", TransportTest,
		iscp.WithConnNodeID("11111111-1111-1111-1111-111111111111"),
	)
	require.NoError(t, err)
	defer conn.Close(ctx)

	up, err := conn.OpenUpstream(ctx,
		"session_id",
		WithUpstreamAckInterval(time.Millisecond),
		WithUpstreamQoS(message.QoSReliable),
	)
	require.NoError(t, err)
	defer up.Close(ctx)

	// WriteChunk with no groups should return nil and send nothing
	err = up.WriteChunk(ctx)
	require.NoError(t, err)

	// Sequence number should still be 0 (no chunk sent)
	assert.Equal(t, uint32(0), up.State().LastIssuedSequenceNumber)
}

func TestUpstream_WriteChunkStreamClosed(t *testing.T) {
	defer goleak.VerifyNone(t)
	d := newDialer(transport.NegotiationParams{})
	RegisterDialer(TransportTest, func() transport.Dialer { return d })
	done := make(chan struct{})
	defer func() {
		<-done
	}()
	go func() {
		defer close(done)
		mockConnectRequest(t, d.srv)
		upstreamOpenReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamOpenRequest)
		mustWrite(t, d.srv, &message.UpstreamOpenResponse{
			RequestID:             upstreamOpenReq.RequestID,
			AssignedStreamID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			AssignedStreamIDAlias: 1,
			ResultCode:            message.ResultCodeSucceeded,
			ResultString:          "OK",
			DataIDAliases:         map[uint32]*message.DataID{},
		})

		closeReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamCloseRequest)
		mustWrite(t, d.srv, &message.UpstreamCloseResponse{
			RequestID:    closeReq.RequestID,
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "OK",
		})
		assert.Equal(t, &message.Disconnect{
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "NormalClosure",
		}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}))
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := Connect("dummy", TransportTest,
		iscp.WithConnNodeID("11111111-1111-1111-1111-111111111111"),
	)
	require.NoError(t, err)
	defer conn.Close(ctx)

	up, err := conn.OpenUpstream(ctx,
		"session_id",
		WithUpstreamAckInterval(time.Millisecond),
		WithUpstreamQoS(message.QoSReliable),
	)
	require.NoError(t, err)

	err = up.Close(ctx)
	require.NoError(t, err)

	// WriteChunk on closed stream should return ErrStreamClosed
	err = up.WriteChunk(ctx,
		&DataPointGroup{
			DataID:     &message.DataID{Name: "name", Type: "type"},
			DataPoints: DataPoints{{ElapsedTime: time.Second, Payload: []byte{1}}},
		},
	)
	assert.Equal(t, errors.ErrStreamClosed, err)
}

func TestUpstream_WriteChunkTotalDataPoints(t *testing.T) {
	defer goleak.VerifyNone(t)
	d := newDialer(transport.NegotiationParams{})
	RegisterDialer(TransportTest, func() transport.Dialer { return d })
	done := make(chan struct{})
	defer func() {
		<-done
	}()
	go func() {
		defer close(done)
		mockConnectRequest(t, d.srv)
		upstreamOpenReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamOpenRequest)
		mustWrite(t, d.srv, &message.UpstreamOpenResponse{
			RequestID:             upstreamOpenReq.RequestID,
			AssignedStreamID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			AssignedStreamIDAlias: 1,
			ResultCode:            message.ResultCodeSucceeded,
			ResultString:          "OK",
			DataIDAliases:         map[uint32]*message.DataID{},
		})

		chunk := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamChunk)
		mustWrite(t, d.srv, &message.UpstreamChunkAck{
			StreamIDAlias: 1,
			Results: []*message.UpstreamChunkResult{
				{SequenceNumber: chunk.StreamChunk.SequenceNumber, ResultCode: message.ResultCodeSucceeded, ResultString: "OK"},
			},
			DataIDAliases:   map[uint32]*message.DataID{},
			ExtensionFields: &message.UpstreamChunkAckExtensionFields{},
		})

		closeReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamCloseRequest)
		assert.Equal(t, uint64(3), closeReq.TotalDataPoints)
		mustWrite(t, d.srv, &message.UpstreamCloseResponse{
			RequestID:    closeReq.RequestID,
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "OK",
		})
		assert.Equal(t, &message.Disconnect{
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "NormalClosure",
		}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}))
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := Connect("dummy", TransportTest,
		iscp.WithConnNodeID("11111111-1111-1111-1111-111111111111"),
	)
	require.NoError(t, err)
	defer conn.Close(ctx)

	up, err := conn.OpenUpstream(ctx,
		"session_id",
		WithUpstreamAckInterval(time.Millisecond),
		WithUpstreamQoS(message.QoSReliable),
	)
	require.NoError(t, err)
	defer up.Close(ctx)

	// Send a chunk with 3 data points across 2 groups
	err = up.WriteChunk(ctx,
		&DataPointGroup{
			DataID: &message.DataID{Name: "name1", Type: "type"},
			DataPoints: DataPoints{
				{ElapsedTime: time.Second, Payload: []byte{1}},
				{ElapsedTime: time.Second * 2, Payload: []byte{2}},
			},
		},
		&DataPointGroup{
			DataID: &message.DataID{Name: "name2", Type: "type"},
			DataPoints: DataPoints{
				{ElapsedTime: time.Second * 3, Payload: []byte{3}},
			},
		},
	)
	require.NoError(t, err)

	// Wait briefly for atomic update to propagate
	assert.Eventually(t, func() bool {
		return up.State().TotalDataPoints == 3
	}, time.Second, time.Millisecond)
}

func TestUpstream_WriteChunkHook(t *testing.T) {
	defer goleak.VerifyNone(t)
	d := newDialer(transport.NegotiationParams{})
	RegisterDialer(TransportTest, func() transport.Dialer { return d })
	done := make(chan struct{})
	defer func() {
		<-done
	}()
	go func() {
		defer close(done)
		mockConnectRequest(t, d.srv)
		upstreamOpenReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamOpenRequest)
		mustWrite(t, d.srv, &message.UpstreamOpenResponse{
			RequestID:             upstreamOpenReq.RequestID,
			AssignedStreamID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			AssignedStreamIDAlias: 1,
			ResultCode:            message.ResultCodeSucceeded,
			ResultString:          "OK",
			DataIDAliases:         map[uint32]*message.DataID{},
		})

		chunk := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamChunk)
		mustWrite(t, d.srv, &message.UpstreamChunkAck{
			StreamIDAlias: 1,
			Results: []*message.UpstreamChunkResult{
				{SequenceNumber: chunk.StreamChunk.SequenceNumber, ResultCode: message.ResultCodeSucceeded, ResultString: "OK"},
			},
			DataIDAliases:   map[uint32]*message.DataID{},
			ExtensionFields: &message.UpstreamChunkAckExtensionFields{},
		})

		closeReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamCloseRequest)
		mustWrite(t, d.srv, &message.UpstreamCloseResponse{
			RequestID:    closeReq.RequestID,
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "OK",
		})
		assert.Equal(t, &message.Disconnect{
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "NormalClosure",
		}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}))
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := Connect("dummy", TransportTest,
		iscp.WithConnNodeID("11111111-1111-1111-1111-111111111111"),
	)
	require.NoError(t, err)
	defer conn.Close(ctx)

	hookedCh := make(chan UpstreamChunk, 1)
	up, err := conn.OpenUpstream(ctx,
		"session_id",
		WithUpstreamAckInterval(time.Millisecond),
		WithUpstreamQoS(message.QoSReliable),
		WithUpstreamSendDataPointsHooker(SendDataPointsHookerFunc(func(streamID uuid.UUID, chunk UpstreamChunk) {
			select {
			case hookedCh <- chunk:
			default:
			}
		})),
	)
	require.NoError(t, err)
	defer up.Close(ctx)

	groups := []*DataPointGroup{
		{
			DataID:     &message.DataID{Name: "name", Type: "type"},
			DataPoints: DataPoints{{ElapsedTime: time.Second, Payload: []byte{1, 2, 3}}},
		},
	}
	err = up.WriteChunk(ctx, groups...)
	require.NoError(t, err)

	hookedChunk := <-hookedCh
	assert.Equal(t, uint32(1), hookedChunk.SequenceNumber)
	require.Len(t, hookedChunk.DataPointGroups, 1)
	assert.Equal(t, &message.DataID{Name: "name", Type: "type"}, hookedChunk.DataPointGroups[0].DataID)
}

// TestUpstream_WriteChunkSequenceOrderManyChunks は、WriteChunk の呼び出し完了順と
// ワイヤ上の送信順（サーバーへの到着順）が一致することを検証する regression テスト。
//
// シーケンス番号の採番は u.mu 内で直列化されているが、実送信を chunk ごとの
// goroutine（go u.sendChunkAndWaitAck）に投げっぱなしにすると、ほぼ同時に起動された
// goroutine の実行順序が入れ替わり、採番順と逆の順序で SendUpstreamChunk が
// 呼ばれることがある（TestUpstream_WriteChunkSequenceShared が -race 実行時に
// 約 2〜6% の頻度で FAIL していた flaky の真因）。
//
// 単発の逆転は確率的にしか踏めないため、chunk を連続送信して 1 回でも隣接逆転が
// 起きたら FAIL する形で検出感度を上げている。
func TestUpstream_WriteChunkSequenceOrderManyChunks(t *testing.T) {
	defer goleak.VerifyNone(t)
	const chunkCount = 100

	d := newDialer(transport.NegotiationParams{})
	RegisterDialer(TransportTest, func() transport.Dialer { return d })
	done := make(chan struct{})
	defer func() {
		<-done
	}()
	go func() {
		defer close(done)
		mockConnectRequest(t, d.srv)
		upstreamOpenReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamOpenRequest)
		mustWrite(t, d.srv, &message.UpstreamOpenResponse{
			RequestID:             upstreamOpenReq.RequestID,
			AssignedStreamID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			AssignedStreamIDAlias: 1,
			ResultCode:            message.ResultCodeSucceeded,
			ResultString:          "OK",
			DataIDAliases:         map[uint32]*message.DataID{},
		})

		for i := 0; i < chunkCount; i++ {
			chunk := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamChunk)
			assert.Equal(t, uint32(i+1), chunk.StreamChunk.SequenceNumber,
				"wire arrival order must match sequence numbering order (i=%d)", i)
			mustWrite(t, d.srv, &message.UpstreamChunkAck{
				StreamIDAlias: 1,
				Results: []*message.UpstreamChunkResult{
					{SequenceNumber: chunk.StreamChunk.SequenceNumber, ResultCode: message.ResultCodeSucceeded, ResultString: "OK"},
				},
				DataIDAliases:   map[uint32]*message.DataID{},
				ExtensionFields: &message.UpstreamChunkAckExtensionFields{},
			})
		}

		closeReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamCloseRequest)
		assert.Equal(t, uint32(chunkCount), closeReq.FinalSequenceNumber)
		mustWrite(t, d.srv, &message.UpstreamCloseResponse{
			RequestID:    closeReq.RequestID,
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "OK",
		})
		assert.Equal(t, &message.Disconnect{
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "NormalClosure",
		}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}))
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := Connect("dummy", TransportTest,
		iscp.WithConnNodeID("11111111-1111-1111-1111-111111111111"),
	)
	require.NoError(t, err)
	defer conn.Close(ctx)

	up, err := conn.OpenUpstream(ctx,
		"session_id",
		WithUpstreamAckInterval(time.Millisecond),
		WithUpstreamQoS(message.QoSReliable),
		WithUpstreamFlushPolicyImmediately(),
	)
	require.NoError(t, err)
	defer up.Close(ctx)

	for i := 0; i < chunkCount; i++ {
		err := up.WriteChunk(ctx,
			&DataPointGroup{
				DataID:     &message.DataID{Name: "name", Type: "type"},
				DataPoints: DataPoints{{ElapsedTime: time.Duration(i+1) * time.Second, Payload: []byte{byte(i)}}},
			},
		)
		require.NoError(t, err)
	}

	assert.Equal(t, uint32(chunkCount), up.State().LastIssuedSequenceNumber)
}

// TestUpstream_Close_CloseTimeoutIsTotalBudget は、closeTimeout が Close 全体の
// 上限として扱われることを検証する。Close には drain 待ち
// （waitToSendAllDataPointsAndReceiveAllAck）とチケットチェーン待ちの 2 つの
// 待機があり、それぞれが独立に closeTimeout を消費すると Close が最大 2 倍
// 待ってしまう。drain 待ちが期限まで使い切った場合、チケット待ちは残り時間 0 で
// 即座に打ち切られるべき。
func TestUpstream_Close_CloseTimeoutIsTotalBudget(t *testing.T) {
	defer goleak.VerifyNone(t)
	d := newDialer(transport.NegotiationParams{})
	RegisterDialer(TransportTest, func() transport.Dialer { return d })

	var chunkReceived atomic.Uint64
	srvDone := make(chan struct{})
	go func() {
		defer close(srvDone)
		srv := d.srv
		for {
			msg, err := srv.ReadMessage()
			if err != nil {
				return
			}
			switch m := msg.(type) {
			case *message.ConnectRequest:
				_ = srv.WriteMessage(&message.ConnectResponse{
					RequestID:       m.RequestID,
					ProtocolVersion: "3.0.0",
					ResultCode:      message.ResultCodeSucceeded,
					ExtensionFields: &message.ConnectResponseExtensionFields{},
				})
			case *message.UpstreamOpenRequest:
				_ = srv.WriteMessage(&message.UpstreamOpenResponse{
					RequestID:             m.RequestID,
					AssignedStreamID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
					AssignedStreamIDAlias: 1,
					ResultCode:            message.ResultCodeSucceeded,
					ResultString:          "OK",
					DataIDAliases:         map[uint32]*message.DataID{},
				})
			case *message.UpstreamChunk:
				// Ack を返さない: drain 待ちに closeTimeout を使い切らせる
				chunkReceived.Add(1)
			case *message.UpstreamCloseRequest:
				_ = srv.WriteMessage(&message.UpstreamCloseResponse{
					RequestID:    m.RequestID,
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "OK",
				})
			case *message.Ping:
				_ = srv.WriteMessage(&message.Pong{RequestID: m.RequestID})
			case *message.Disconnect:
				return
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := Connect("dummy", TransportTest,
		iscp.WithConnNodeID("11111111-1111-1111-1111-111111111111"),
	)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, conn.Close(ctx))
		<-srvDone
	}()

	closeTimeout := time.Second
	up, err := conn.OpenUpstream(ctx, "session_id",
		WithUpstreamQoS(message.QoSReliable),
		WithUpstreamFlushPolicyImmediately(),
		WithUpstreamCloseTimeout(closeTimeout),
	)
	require.NoError(t, err)

	require.NoError(t, up.WriteChunk(ctx, &DataPointGroup{
		DataID:     &message.DataID{Name: "name", Type: "type"},
		DataPoints: DataPoints{{ElapsedTime: time.Second, Payload: []byte{1}}},
	}))
	// chunk がサーバーに到達する（= 送信試行が完了しチケットが閉じる）まで待つ。
	// これ以降 drain 待ちは「Ack が来ない chunk が 1 つ残っている」状態で確定する。
	require.Eventually(t, func() bool { return chunkReceived.Load() == 1 }, 5*time.Second, time.Millisecond)

	// 「送信試行が完了しない chunk が in-flight にある」状態を模擬し、
	// チケット待ちも期限まで解けないようにする。
	up.BlockSendTicketChain(t)

	start := time.Now()
	err = up.Close(ctx)
	elapsed := time.Since(start)
	require.NoError(t, err)
	// drain 待ちが closeTimeout(1s) を使い切る。チケット待ちが独立に
	// closeTimeout を取ると計 2 秒待つが、共有 budget なら計 1 秒で返る。
	assert.GreaterOrEqual(t, elapsed, 900*time.Millisecond)
	assert.Less(t, elapsed, 1600*time.Millisecond)
}
