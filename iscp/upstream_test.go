package iscp_test

import (
	"context"
	stdlog "log"
	"math"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/aptpod/iscp-go/errors"
	"github.com/aptpod/iscp-go/iscp"
	. "github.com/aptpod/iscp-go/iscp"
	"github.com/aptpod/iscp-go/log"
	"github.com/aptpod/iscp-go/message"
	"github.com/aptpod/iscp-go/transport"
	uuid "github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

type CaptureHooker struct {
	afterReceivedAckCh chan struct {
		StreamID      uuid.UUID
		Sequence      uint32
		DataPointsAck DataPointsAck
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
			StreamID      uuid.UUID
			Sequence      uint32
			DataPointsAck DataPointsAck
		}, 1024),
		beforeSendDataPointsCh: make(chan struct {
			StreamID   uuid.UUID
			Sequence   uint32
			DataPoints DataPointGroups
		}, 1024),
	}
}

func (c *CaptureHooker) HookAfter(streamID uuid.UUID, ack UpstreamChunkAck) {
	select {
	case c.afterReceivedAckCh <- struct {
		StreamID      uuid.UUID
		Sequence      uint32
		DataPointsAck DataPointsAck
	}{
		StreamID:      streamID,
		Sequence:      ack.SequenceNumber,
		DataPointsAck: ack.DataPointsAck,
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

				assert.Equal(t, &message.UpstreamCloseRequest{
					RequestID:           6,
					StreamID:            uuid.MustParse("11111111-1111-1111-1111-111111111111"),
					TotalDataPoints:     1,
					FinalSequenceNumber: 1,
					ExtensionFields:     &message.UpstreamCloseRequestExtensionFields{},
				}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}))
				mustWrite(t, d.srv, &message.UpstreamCloseResponse{
					RequestID:    6,
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
				iscp.WithConnPingInterval(time.Second),
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
			assert.Equal(t, message.ResultCodeSucceeded, ack.DataPointsAck.ResultCode)
			assert.Equal(t, &DataPointsAck{
				ResultCode:   message.ResultCodeSucceeded,
				ResultString: "OK",
			}, &ack.DataPointsAck)
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

				assert.Equal(t, &message.UpstreamCloseRequest{
					RequestID:           6,
					StreamID:            uuid.MustParse("11111111-1111-1111-1111-111111111111"),
					TotalDataPoints:     1,
					FinalSequenceNumber: 1,
					ExtensionFields:     &message.UpstreamCloseRequestExtensionFields{},
				}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}))
				mustWrite(t, d.srv, &message.UpstreamCloseResponse{
					RequestID:    6,
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
			assert.Equal(t, message.ResultCodeSucceeded, ack.DataPointsAck.ResultCode)
			assert.Equal(t, &DataPointsAck{
				ResultCode:   message.ResultCodeSucceeded,
				ResultString: "OK",
			}, &ack.DataPointsAck)
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

				assert.Equal(t, &message.UpstreamCloseRequest{
					RequestID:           6,
					StreamID:            uuid.MustParse("11111111-1111-1111-1111-111111111111"),
					TotalDataPoints:     1,
					FinalSequenceNumber: 1,
					ExtensionFields:     &message.UpstreamCloseRequestExtensionFields{},
				}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}))
				mustWrite(t, d.srv, &message.UpstreamCloseResponse{
					RequestID:    6,
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
			assert.Equal(t, message.ResultCodeSucceeded, ack.DataPointsAck.ResultCode)
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
			assert.Equal(t, message.ResultCodeSucceeded, ack.DataPointsAck.ResultCode)
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
			assert.Equal(t, message.ResultCodeSucceeded, ack.DataPointsAck.ResultCode)
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
			assert.Equal(t, message.ResultCodeSucceeded, ack.DataPointsAck.ResultCode)
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
			assert.Equal(t, message.ResultCodeSucceeded, ack.DataPointsAck.ResultCode)
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

func TestUpstream_Resume(t *testing.T) {
	nodeID := "11111111-1111-1111-1111-111111111111"
	defer goleak.VerifyNone(t)
	ds := []*dialer{newDialer(transport.NegotiationParams{}), newDialer(transport.NegotiationParams{})}
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
					TotalDataPoints:     1000,
					FinalSequenceNumber: 1000,
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
	ctx := context.Background()
	conn, err := Connect("dummy", TransportTest,
		iscp.WithConnNodeID(nodeID),
		iscp.WithConnPingInterval(time.Second),
		iscp.WithConnLogger(log.NewStdWith(stdlog.New(os.Stderr, "SERVER:", stdlog.LstdFlags))),
	)
	require.NoError(t, err)
	defer conn.Close(ctx)

	gotCh := make(chan DataPointsAck, 1024)

	dataPointCount := 1000
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
		WithUpstreamReceiveAckHooker(ReceiveAckHookerFunc(func(streamID uuid.UUID, ack UpstreamChunkAck) {
			gotCh <- ack.DataPointsAck
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
	for i := 0; i < dataPointCount; i++ {
		require.NoError(t, up.WriteDataPoints(ctx, &message.DataID{
			Name: "name",
			Type: "string",
		}, &message.DataPoint{
			ElapsedTime: 1,
			Payload:     []byte("hello-world"),
		}))
		if i == 0 {
			ds[0].Close()
		}
	}

	gotSeqNumsCond.L.Lock()
	for len(gotSeqNums) != dataPointCount {
		gotSeqNumsCond.Wait()
	}
	gotSeqNumsCond.L.Unlock()

	for i, v := range gotSeqNums {
		assert.EqualValues(t, i+1, v, "want:%d got:%d", i+1, v)
	}
	assert.EqualValues(t, up.State().LastIssuedSequenceNumber, len(gotSeqNums))
	assert.EqualValues(t, up.State().TotalDataPoints, dataPointCount)

	go func() {
		for {
			if err := up.WriteDataPoints(ctx, &message.DataID{
				Name: "name",
				Type: "string",
			}, &message.DataPoint{
				ElapsedTime: 1,
				Payload:     []byte("hello-world"),
			}); err != nil {
				return
			}
			time.Sleep(time.Millisecond * 10)
		}
	}()
	assert.Equal(t, DataPointsAck{
		ResultCode:   message.ResultCodeSucceeded,
		ResultString: "OK",
	}, <-gotCh)
	up.Close(ctx)
	gotResumedEvent := <-resumedEvCh
	assert.GreaterOrEqual(t, gotResumedEvent.State.TotalDataPoints, uint64(1))
	assert.GreaterOrEqual(t, gotResumedEvent.State.LastIssuedSequenceNumber, uint32(1))

	gotClosedEvent := <-closedEvCh
	assert.Equal(t, up.State(), &gotClosedEvent.State)
}

func TestUpstream_Resume_Failure(t *testing.T) {
	defer goleak.VerifyNone(t)
	ds := []*dialer{newDialer(transport.NegotiationParams{}), newDialer(transport.NegotiationParams{})}
	var callCount int
	RegisterDialer(TransportTest, func() transport.Dialer {
		callCount++
		time.Sleep(time.Duration(callCount) * time.Millisecond * 10)
		return ds[callCount-1]
	},
	)
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
		iscp.WithConnPingInterval(time.Second),
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
