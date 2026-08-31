package iscp_test

import (
	"context"
	"fmt"
	stdlog "log"
	"math"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	uuid "github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/aptpod/iscp-go/errors"
	"github.com/aptpod/iscp-go/iscp"
	. "github.com/aptpod/iscp-go/iscp"
	"github.com/aptpod/iscp-go/log"
	"github.com/aptpod/iscp-go/message"
	"github.com/aptpod/iscp-go/transport"
	"github.com/aptpod/iscp-go/wire"
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
				upstreamOpenReq := mustReadIgnorePingPong(t, d.srv).(*message.UpstreamOpenRequest)
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

				chunk := mustReadIgnorePingPong(t, d.srv).(*message.UpstreamChunk)
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
				}, mustReadIgnorePingPong(t, d.srv))
				mustWrite(t, d.srv, &message.UpstreamCloseResponse{
					RequestID:    6,
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "OK",
				})
				assert.Equal(t, &message.Disconnect{
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "NormalClosure",
				}, mustReadIgnorePingPong(t, d.srv))
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
				upstreamOpenReq := mustReadIgnorePingPong(t, d.srv).(*message.UpstreamOpenRequest)
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

				chunk := mustReadIgnorePingPong(t, d.srv).(*message.UpstreamChunk)
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
				}, mustReadIgnorePingPong(t, d.srv))
				mustWrite(t, d.srv, &message.UpstreamCloseResponse{
					RequestID:    6,
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "OK",
				})
				assert.Equal(t, &message.Disconnect{
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "NormalClosure",
				}, mustReadIgnorePingPong(t, d.srv))
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
				upstreamOpenReq := mustReadIgnorePingPong(t, d.srv).(*message.UpstreamOpenRequest)
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

				chunk := mustReadIgnorePingPong(t, d.srv).(*message.UpstreamChunk)
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
				}, mustReadIgnorePingPong(t, d.srv))
				mustWrite(t, d.srv, &message.UpstreamCloseResponse{
					RequestID:    6,
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "OK",
				})
				assert.Equal(t, &message.Disconnect{
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "NormalClosure",
				}, mustReadIgnorePingPong(t, d.srv))
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
				upstreamOpenReq := mustReadIgnorePingPong(t, d.srv).(*message.UpstreamOpenRequest)
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

				chunk := mustReadIgnorePingPong(t, d.srv).(*message.UpstreamChunk)
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
				closeRequest := mustReadIgnorePingPong(t, d.srv).(*message.UpstreamCloseRequest)
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
				}, mustReadIgnorePingPong(t, d.srv))
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
				upstreamOpenReq := mustReadIgnorePingPong(t, d.srv).(*message.UpstreamOpenRequest)
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

				chunk := mustReadIgnorePingPong(t, d.srv).(*message.UpstreamChunk)
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
				closeRequest := mustReadIgnorePingPong(t, d.srv).(*message.UpstreamCloseRequest)
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
				}, mustReadIgnorePingPong(t, d.srv))
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
				upstreamOpenReq := mustReadIgnorePingPong(t, d.srv).(*message.UpstreamOpenRequest)
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

				chunk := mustReadIgnorePingPong(t, d.srv).(*message.UpstreamChunk)
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
				closeRequest := mustReadIgnorePingPong(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamCloseRequest)
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
				}, mustReadIgnorePingPong(t, d.srv, &message.Ping{}, &message.Pong{}))
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
				upstreamOpenReq := mustReadIgnorePingPong(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamOpenRequest)
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

				chunk := mustReadIgnorePingPong(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamChunk)
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
				closeRequest := mustReadIgnorePingPong(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamCloseRequest)
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
				}, mustReadIgnorePingPong(t, d.srv, &message.Ping{}, &message.Pong{}))
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
				upstreamOpenReq := mustReadIgnorePingPong(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamOpenRequest)
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

				chunk := mustReadIgnorePingPong(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamChunk)
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

				closeRequest := mustReadIgnorePingPong(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamCloseRequest)
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
				}, mustReadIgnorePingPong(t, d.srv, &message.Ping{}, &message.Pong{}))
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
				upstreamOpenReq := mustReadIgnorePingPong(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamOpenRequest)
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
				}, mustReadIgnorePingPong(t, d.srv, &message.Ping{}, &message.Pong{}))
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
		msg, ok := mustReadIgnorePingPong(t, d.srv).(*message.UpstreamOpenRequest)
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
		msg := mustReadIgnorePingPong(t, d.srv)
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
			msg := mustReadIgnorePingPong(t, d.srv)
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
		}, mustReadIgnorePingPong(t, d.srv, &message.Ping{}, &message.Pong{}))
	}()
	ctx := context.Background()
	conn, err := Connect("dummy", TransportTest,
		iscp.WithConnNodeID(nodeID),
		iscp.WithConnPingInterval(time.Second),
		iscp.WithConnPingTimeout(time.Second),
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
		msg, ok := mustReadIgnorePingPong(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamOpenRequest)
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
		msg := mustReadIgnorePingPong(t, d.srv, &message.Ping{}, &message.Pong{})
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

		m := mustReadIgnorePingPong(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamCloseRequest)
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
		}, mustReadIgnorePingPong(t, d.srv, &message.Ping{}, &message.Pong{}))
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
				upstreamOpenReq := mustReadIgnorePingPong(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamOpenRequest)
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

				closeRequest := mustReadIgnorePingPong(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamCloseRequest)
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
				}, mustReadIgnorePingPong(t, d.srv, &message.Ping{}, &message.Pong{}))
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
		msg, ok := mustReadIgnorePingPong(t, d.srv).(*message.UpstreamOpenRequest)
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
		msg := mustReadIgnorePingPong(t, d.srv)
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
			msg := mustReadIgnorePingPong(t, d.srv)
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
		}, mustReadIgnorePingPong(t, d.srv, &message.Ping{}, &message.Pong{}))
	}()
	conn, err := Connect("dummy", TransportTest,
		iscp.WithConnNodeID(nodeID),
		iscp.WithConnPingInterval(time.Second),
		iscp.WithConnPingTimeout(time.Millisecond*1000),
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

// TestUpstream_Close_ConcurrentDataIDAliasWrite_NoRace は、Close 経由の
// closeWithError が dataIDAliases をロック無しで走査しないことを検証する
// （データレースの回帰防止）。
//
// 発火条件: 修正前の closeWithError は u.mu を持たずに stateWithoutLock() を
// 呼び、u.mu の下で書き換わる dataIDAliases をロック無しで走査していた。
// closeWithError が CloseRequest を送るまでの間、processDataIDAliases
// （ack 受信）が新規エイリアスを書き込み続けるようサーバー側から送り続ける
// ことで、走査中の書き込みと確実に重ねる。
//
// オラクル: -race 付きで実行して競合が報告されないこと（修正前は
// concurrent map read/write として報告される）。
func TestUpstream_Close_ConcurrentDataIDAliasWrite_NoRace(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	ctx := context.Background()

	d := newDialer(transport.NegotiationParams{})
	RegisterDialer(TransportTest, func() transport.Dialer { return d })

	// d.srv への書き込みをサーバー側の複数 goroutine（メインの読み取り
	// ループと alias spam）から行うため、直列化する。競合検証の対象は
	// あくまでクライアント側（Upstream）の内部状態であり、テストの
	// サーバー側実装を意図的に競合させたいわけではない。
	var writeMu sync.Mutex
	safeWrite := func(msg message.Message) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return d.srv.Write(msg)
	}
	readIgnorePingPong := func() message.Message {
		for {
			msg, err := d.srv.Read()
			require.NoError(t, err)
			if ping, ok := msg.(*message.Ping); ok {
				require.NoError(t, safeWrite(&message.Pong{
					RequestID:       ping.RequestID,
					ExtensionFields: &message.PongExtensionFields{},
				}))
				continue
			}
			if _, ok := msg.(*message.Pong); ok {
				continue
			}
			return msg
		}
	}

	stopAliasSpam := make(chan struct{})
	srvDone := make(chan struct{})
	go func() {
		defer close(srvDone)
		mockConnectRequest(t, d.srv)
		openReq := readIgnorePingPong().(*message.UpstreamOpenRequest)
		require.NoError(t, safeWrite(&message.UpstreamOpenResponse{
			RequestID:             openReq.RequestID,
			AssignedStreamID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			AssignedStreamIDAlias: 1,
			ResultCode:            message.ResultCodeSucceeded,
			ResultString:          "OK",
			DataIDAliases:         map[uint32]*message.DataID{},
		}))

		// closeWithError が dataIDAliases のスナップショットを取り終える
		// まで（＝CloseRequest を送るまで）、新規エイリアスを含む Ack を
		// 送り続ける。処理が readAckLoop → aliasCh → readAliasLoop →
		// processDataIDAliases（u.mu.Lock）まで届き続けている限り、
		// closeWithError 側の走査タイミングと必ず重なる。
		aliasDone := make(chan struct{})
		go func() {
			defer close(aliasDone)
			var n uint32 = 2
			for {
				select {
				case <-stopAliasSpam:
					return
				default:
				}
				_ = safeWrite(&message.UpstreamChunkAck{
					StreamIDAlias: 1,
					DataIDAliases: map[uint32]*message.DataID{
						n: {Name: fmt.Sprintf("name-%d", n), Type: "type"},
					},
					ExtensionFields: &message.UpstreamChunkAckExtensionFields{},
				})
				n++
			}
		}()

		closeReq := readIgnorePingPong().(*message.UpstreamCloseRequest)
		close(stopAliasSpam)
		<-aliasDone
		require.NoError(t, safeWrite(&message.UpstreamCloseResponse{
			RequestID:    closeReq.RequestID,
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "OK",
		}))
		readIgnorePingPong() // Disconnect
	}()

	conn, err := Connect("dummy", TransportTest)
	require.NoError(t, err)

	up, err := conn.OpenUpstream(ctx,
		"session_id",
		WithUpstreamQoS(message.QoSReliable),
	)
	require.NoError(t, err)

	// alias spam が dataIDAliases を書き換え始める猶予を少し与える。
	time.Sleep(5 * time.Millisecond)
	require.NoError(t, up.Close(ctx))
	require.NoError(t, conn.Close(ctx))

	<-srvDone
}

// TestUpstream_Flush_ValidateStateFailure_DoesNotBlockOtherOperationsWhileClosing は、
// flush の validateState 失敗経路が u.mu を保持したまま closeWithError の
// ネットワーク往復（CloseRequest 送信・応答待ち）に入らないことを検証する。
//
// 発火条件: 修正前の flush は defer u.mu.Unlock() の下で closeWithError を
// 呼んでいた。CloseRequest の応答が届くまで u.mu を握り続けるため、
// u.mu を要する他の操作（State() など）が連鎖して止まる。
//
// オラクル: サーバーが CloseResponse を返さず応答待ちを続けている間でも、
// State() が即座に完了すること。修正前は State() がブロックされたまま
// タイムアウトする。
func TestUpstream_Flush_ValidateStateFailure_DoesNotBlockOtherOperationsWhileClosing(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	ctx := context.Background()

	d := newDialer(transport.NegotiationParams{})
	RegisterDialer(TransportTest, func() transport.Dialer { return d })

	closeReqReceived := make(chan *message.UpstreamCloseRequest, 1)
	releaseCloseResp := make(chan struct{})
	srvDone := make(chan struct{})
	go func() {
		defer close(srvDone)
		mockConnectRequest(t, d.srv)
		openReq := mustReadIgnorePingPong(t, d.srv).(*message.UpstreamOpenRequest)
		mustWrite(t, d.srv, &message.UpstreamOpenResponse{
			RequestID:             openReq.RequestID,
			AssignedStreamID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			AssignedStreamIDAlias: 1,
			ResultCode:            message.ResultCodeSucceeded,
			ResultString:          "OK",
			DataIDAliases:         map[uint32]*message.DataID{},
		})

		closeReq := mustReadIgnorePingPong(t, d.srv).(*message.UpstreamCloseRequest)
		closeReqReceived <- closeReq
		<-releaseCloseResp
		mustWrite(t, d.srv, &message.UpstreamCloseResponse{
			RequestID:    closeReq.RequestID,
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "OK",
		})
		mustReadIgnorePingPong(t, d.srv) // Disconnect
	}()

	conn, err := Connect("dummy", TransportTest)
	require.NoError(t, err)

	up, err := conn.OpenUpstream(ctx,
		"session_id",
		WithUpstreamQoS(message.QoSReliable),
		WithUpstreamFlushPolicyImmediately(),
	)
	require.NoError(t, err)

	// 次の flush で validateState が必ず失敗するようにする。
	up.SetSequenceNumber(t, math.MaxUint32)

	dataID := &message.DataID{Name: "name", Type: "type"}
	dp := &message.DataPoint{ElapsedTime: time.Second, Payload: []byte{1, 2, 3, 4}}
	require.NoError(t, up.WriteDataPoints(ctx, dataID, dp))

	select {
	case <-closeReqReceived:
	case <-time.After(2 * time.Second):
		t.Fatal("closeWithError was not triggered by validateState failure")
	}

	// closeWithError が CloseResponse 待ちでブロックしている間、u.mu を
	// 要する他の操作が妨げられないことを確認する。
	stateDone := make(chan struct{})
	go func() {
		up.State()
		close(stateDone)
	}()
	select {
	case <-stateDone:
	case <-time.After(2 * time.Second):
		t.Fatal("State() blocked while closeWithError waits for CloseResponse: flush holds u.mu across the network round trip")
	}

	close(releaseCloseResp)

	// run() の完了（closeWithErrorBounded 側の SendUpstreamCloseRequest
	// 完了 → u.cancel() 発火）を待ってから conn.Close(ctx) を呼ぶ。
	// これを待たずに conn.Close(ctx) を呼ぶと、wire.ClientConn 側の
	// readUpstreamChunkAckLoop の終了処理（acks map の走査、ロック無し）
	// と SendUpstreamCloseRequest 内の acks map からの削除（ロック下）が
	// 競合しうる（wire/client_conn.go 側の別の既知の問題であり、本タスクの
	// owned paths 外）。
	select {
	case <-up.WaitRunDoneForTest():
	case <-time.After(2 * time.Second):
		t.Fatal("run() did not complete after CloseResponse was sent")
	}

	require.NoError(t, conn.Close(ctx))
	<-srvDone
}

// TestUpstream_FlushValidateFailureThenClose_SendsCloseRequestOnce は、
// flush の validateState 失敗による内部エラー経路と、利用者からの Close
// 呼び出しが並行に重なっても CloseRequest が 1 回しか送られないことを
// 検証する（closeWithError の多重呼び出しガードの回帰防止）。
//
// 発火条件: 修正前のガードは isClosed()（u.ctx.Done）のみで、u.cancel() は
// 勝者の return 時まで発火しない。そのため、内部エラー経路が
// CloseRequest の応答待ちをしている間に Close() が呼ばれると、isClosed()
// はまだ false のままなので Close() 側も closeWithError の本体（2 回目の
// CloseRequest 送信）に入ってしまう。
//
// なお Close() は closeWithError の前に waitToSendAllDataPointsAndReceiveAllAck
// 経由で Flush(ctx) を呼ぶため、flushLoop（内部エラー経路の勝者）が
// CloseResponse 待ちでブロックしている間は Close() 自身もブロックされる
// （flushLoop は単一 goroutine の for-select であり、flush() 実行中は
// explicitlyFlushCh を消費できないため）。したがって Close() が返るのは
// 勝者の tear-down 完了（u.ctx のキャンセル）後になる。
//
// オラクル: サーバーが 2 通目の CloseRequest を受け取らないこと、
// および勝者の tear-down 完了後、Close(ctx) が isClosed() の早期 return で
// 直ちに（2 回目の CloseRequest を送らずに）nil を返すこと。
func TestUpstream_FlushValidateFailureThenClose_SendsCloseRequestOnce(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	ctx := context.Background()

	d := newDialer(transport.NegotiationParams{})
	RegisterDialer(TransportTest, func() transport.Dialer { return d })

	closeReqReceived := make(chan *message.UpstreamCloseRequest, 1)
	releaseCloseResp := make(chan struct{})
	srvDone := make(chan struct{})
	go func() {
		defer close(srvDone)
		mockConnectRequest(t, d.srv)
		openReq := mustReadIgnorePingPong(t, d.srv).(*message.UpstreamOpenRequest)
		mustWrite(t, d.srv, &message.UpstreamOpenResponse{
			RequestID:             openReq.RequestID,
			AssignedStreamID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			AssignedStreamIDAlias: 1,
			ResultCode:            message.ResultCodeSucceeded,
			ResultString:          "OK",
			DataIDAliases:         map[uint32]*message.DataID{},
		})

		closeReq := mustReadIgnorePingPong(t, d.srv).(*message.UpstreamCloseRequest)
		closeReqReceived <- closeReq
		<-releaseCloseResp
		mustWrite(t, d.srv, &message.UpstreamCloseResponse{
			RequestID:    closeReq.RequestID,
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "OK",
		})

		// 2 回目の CloseRequest が来たら多重送信のバグ。Disconnect 以外の
		// メッセージが届いたら失敗させる。
		msg := mustReadIgnorePingPong(t, d.srv)
		if _, ok := msg.(*message.Disconnect); !ok {
			t.Errorf("unexpected message after close, want Disconnect: got %T", msg)
		}
	}()

	conn, err := Connect("dummy", TransportTest)
	require.NoError(t, err)

	up, err := conn.OpenUpstream(ctx,
		"session_id",
		WithUpstreamQoS(message.QoSReliable),
		WithUpstreamFlushPolicyImmediately(),
	)
	require.NoError(t, err)

	// 次の flush で validateState が必ず失敗するようにする。
	up.SetSequenceNumber(t, math.MaxUint32)

	dataID := &message.DataID{Name: "name", Type: "type"}
	dp := &message.DataPoint{ElapsedTime: time.Second, Payload: []byte{1, 2, 3, 4}}
	require.NoError(t, up.WriteDataPoints(ctx, dataID, dp))

	select {
	case <-closeReqReceived:
	case <-time.After(2 * time.Second):
		t.Fatal("closeWithError was not triggered by validateState failure")
	}

	// 内部経路がまだ CloseResponse を待っている間に Close() を呼ぶ。
	// Close() は内部で Flush を経由するため、勝者の tear-down が完了する
	// まではブロックされ続けるはず（2 回目の CloseRequest を送って早期に
	// 返ってしまわないことを、まずここで確認する）。
	closeDone := make(chan error, 1)
	go func() {
		closeDone <- up.Close(ctx)
	}()
	select {
	case err := <-closeDone:
		t.Fatalf("Close returned before the internal close path completed: %v", err)
	case <-time.After(200 * time.Millisecond):
	}

	// 勝者の tear-down を完了させる。敗者の Close() は isClosed() の
	// 早期 return で、2 回目の CloseRequest を送らずに直ちに返るはず。
	close(releaseCloseResp)
	select {
	case err := <-closeDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return after the internal close path completed")
	}

	require.NoError(t, conn.Close(ctx))
	<-srvDone
}

// TestUpstream_FlushValidateStateFailure_ClosesWithinCloseTimeoutWhenCloseResponseNeverArrives は、
// 内部エラー経路（flush の validateState 失敗）から呼ばれる closeWithError
// が closeTimeout を超えて待たないことを検証する。
//
// 発火条件: closeWithError の多重呼び出しガード導入後、u.cancel() を呼べる
// のは勝者の defer のみになる。内部経路が u.ctx をそのまま渡すと、
// CloseResponse が永遠に届かない場合、u.ctx の解除者（cancel）自身の
// return を u.ctx で待つ自己参照になり、upstream が畳まれないまま残る。
// closeWithErrorBounded による closeTimeout の上限がこれを切る。
//
// オラクル: CloseResponse が一切届かなくても、closeTimeout + 余裕以内に
// run() が終了（u.cancel() が発火）すること。
func TestUpstream_FlushValidateStateFailure_ClosesWithinCloseTimeoutWhenCloseResponseNeverArrives(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	ctx := context.Background()

	d := newDialer(transport.NegotiationParams{})
	RegisterDialer(TransportTest, func() transport.Dialer { return d })

	closeReqReceived := make(chan struct{})
	srvDone := make(chan struct{})
	go func() {
		defer close(srvDone)
		mockConnectRequest(t, d.srv)
		openReq := mustReadIgnorePingPong(t, d.srv).(*message.UpstreamOpenRequest)
		mustWrite(t, d.srv, &message.UpstreamOpenResponse{
			RequestID:             openReq.RequestID,
			AssignedStreamID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			AssignedStreamIDAlias: 1,
			ResultCode:            message.ResultCodeSucceeded,
			ResultString:          "OK",
			DataIDAliases:         map[uint32]*message.DataID{},
		})

		_ = mustReadIgnorePingPong(t, d.srv).(*message.UpstreamCloseRequest)
		close(closeReqReceived)
		// CloseResponse を一切返さない。
	}()

	conn, err := Connect("dummy", TransportTest)
	require.NoError(t, err)
	defer conn.Close(ctx)

	closeTimeout := 300 * time.Millisecond
	up, err := conn.OpenUpstream(ctx,
		"session_id",
		WithUpstreamQoS(message.QoSReliable),
		WithUpstreamFlushPolicyImmediately(),
		WithUpstreamCloseTimeout(closeTimeout),
	)
	require.NoError(t, err)

	// 次の flush で validateState が必ず失敗するようにする。
	up.SetSequenceNumber(t, math.MaxUint32)

	dataID := &message.DataID{Name: "name", Type: "type"}
	dp := &message.DataPoint{ElapsedTime: time.Second, Payload: []byte{1, 2, 3, 4}}
	require.NoError(t, up.WriteDataPoints(ctx, dataID, dp))

	select {
	case <-closeReqReceived:
	case <-time.After(2 * time.Second):
		t.Fatal("closeWithError was not triggered by validateState failure")
	}

	select {
	case <-up.WaitRunDoneForTest():
	case <-time.After(closeTimeout + 2*time.Second):
		t.Fatal("internal closeWithError did not respect closeTimeout: possible self-referential wait on u.ctx")
	}
}

// TestUpstream_Close_FlushRespectsCloseTimeoutWhenFlushLoopAbsent は、Close が
// waitToSendAllDataPointsAndReceiveAllAck 内で呼ぶ Flush に closeTimeout 由来の
// 期限が効くことを検証する（Flush での無期限ハングの回帰防止）。
//
// 発火条件: flushLoop は run() の errgroup メンバなので、切断中（次の
// resume/run() が flushLoop を再起動するまでの間）は存在しない。Flush は
// unbuffered な explicitlyFlushCh へ送ろうとして受信者（flushLoop）を待つ。
// u.ctx は Close が呼ばれるまで cancel されない。したがって、flushLoop が
// 居ない間に streamStatusResuming 以外の状態で Close が呼ばれると、Flush に
// 期限が無ければ永久に待つ。
//
// この窓（Close の streamState 遷移が run() 内の streamStatusResuming への
// 遷移より先であること）は、Conn 経由の実際の切断・resume では極めて狭く、
// sleep や実際の切断タイミングでは確定的に再現できない
// （connState は Conn と共有されており、Conn の再接続ロジックにまで
// 影響するため directly 操作もできない）。本テストでは
// NewUpstreamForTest で Conn を介さず Upstream を単体構築し、Upstream 専用
// （Conn と非共有）の connState を直接操作して run() を確実に終了させる
// （flushLoop の不在を WaitRunDoneForTest で確定的に確認）。その後
// streamState を streamStatusConnected へ戻してから Close を呼ぶことで、Conn
// の resume 経路（resume 成功で streamStatusConnected へ戻ってから、次の
// run() が flushLoop を再起動するまでの間）で起こりうる同じ状況を、sleep に
// 頼らず確定的に再現する。
//
// オラクル: flushLoop が存在しない状態で Close を呼んでも、closeTimeout +
// 余裕以内に返ること。修正前は u.Flush(ctx) を無期限の ctx で呼んでいたため
// 無期限にハングする。
func TestUpstream_Close_FlushRespectsCloseTimeoutWhenFlushLoopAbsent(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	ctx := context.Background()

	cli, srv := Pipe()

	closeReqReceived := make(chan struct{})
	srvDone := make(chan struct{})
	go func() {
		defer close(srvDone)
		mockConnectRequest(t, srv)
		closeReq := mustReadIgnorePingPong(t, srv).(*message.UpstreamCloseRequest)
		close(closeReqReceived)
		mustWrite(t, srv, &message.UpstreamCloseResponse{
			RequestID:    closeReq.RequestID,
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "OK",
		})
	}()

	wireConn, err := wire.Connect(&wire.ClientConnConfig{
		Transport:       cli,
		ProtocolVersion: "3.0.0",
		NodeID:          "11111111-1111-1111-1111-111111111111",
	})
	require.NoError(t, err)

	closeTimeout := 1 * time.Second
	up := NewUpstreamForTest(wireConn, uuid.MustParse("22222222-2222-2222-2222-222222222222"), 1, closeTimeout)
	up.RunForTest(false)

	// flushLoop 不在を確定的に作る: connState（Upstream 専用の独立インスタンス）
	// を直接 Reconnecting にスワップし、run() 内の goroutine（connState の変化を
	// 監視している）に streamStatusResuming への遷移と errgroup ctx の
	// キャンセルを行わせ、run() 全体（flushLoop を含む）を終了させる。この世代の
	// run() の完了を WaitRunDoneForTest で待つことで、flushLoop が確実に不在に
	// なったことを sleep なしで確認する。
	up.ConnStateForTest().Swap(ConnStatusReconnecting)
	select {
	case <-up.WaitRunDoneForTest():
	case <-time.After(2 * time.Second):
		t.Fatal("run() did not terminate after connState became Reconnecting")
	}

	// streamState を streamStatusConnected に戻す。Conn の resume 経路では
	// resume() 成功時に streamStatusConnected へ戻り、その直後に次の run() が
	// flushLoop を再起動する。この2つの間の窓を、Conn の resume 処理を
	// 経由せずに直接再現する。
	before := up.SetStreamStateForTest(StreamStatusConnected)
	require.Equal(t, StreamStatusResuming, before)

	closeDone := make(chan error, 1)
	start := time.Now()
	go func() {
		closeDone <- up.Close(ctx)
	}()

	select {
	case err := <-closeDone:
		require.NoError(t, err)
		elapsed := time.Since(start)
		// Flush が closeTimeout で打ち切られた後、closeWithError の
		// CloseRequest/Response の往復が続くため、closeTimeout をやや
		// 超えるのは正常。無期限ハングと区別できるだけの上限を設ける。
		assert.Less(t, elapsed, closeTimeout+5*time.Second)
	case <-time.After(closeTimeout + 5*time.Second):
		t.Fatal("Close hung indefinitely waiting for Flush: flushCtx deadline was not applied")
	}

	<-closeReqReceived
	<-srvDone
	require.NoError(t, wireConn.Close())
}
