package iscp_test

import (
	"context"
	"sync"
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

func TestDownstream_ReadDataPoint(t *testing.T) {
	nodeID := "11111111-1111-1111-1111-111111111111"
	info := &message.UpstreamInfo{
		SessionID:    "session_id",
		SourceNodeID: nodeID,
		StreamID:     uuid.MustParse("121b8205-e7cf-4e22-8b23-48d834de8c2c"),
	}
	dataID := &message.DataID{
		Name: "1",
		Type: "1",
	}
	dataPoint := &message.DataPoint{
		ElapsedTime: time.Second,
		Payload:     []byte{1, 2, 3, 4},
	}
	seq := uint32(1)
	want := &DownstreamChunk{
		SequenceNumber: seq,
		DataPointGroups: []*DataPointGroup{
			{
				DataID: dataID,
				DataPoints: iscp.DataPoints{
					{
						ElapsedTime: dataPoint.ElapsedTime,
						Payload:     dataPoint.Payload,
					},
				},
			},
		},
		UpstreamInfo: info,
	}
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
				downstreamOpenReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.DownstreamOpenRequest)
				assert.Equal(t, &message.DownstreamOpenRequest{
					RequestID:            downstreamOpenReq.RequestID,
					DesiredStreamIDAlias: 2,
					DownstreamFilters: []*message.DownstreamFilter{
						{
							SourceNodeID: nodeID,
							DataFilters: []*message.DataFilter{
								{
									Name: "#",
									Type: "#",
								},
							},
						},
					},
					ExpiryInterval: time.Minute,
					DataIDAliases:  map[uint32]*message.DataID{},
					QoS:            tt.qos,
				}, downstreamOpenReq)
				mustWrite(t, d.srv, &message.DownstreamOpenResponse{
					RequestID:        downstreamOpenReq.RequestID,
					AssignedStreamID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
					ResultCode:       message.ResultCodeSucceeded,
					ResultString:     "OK",
					ExtensionFields:  &message.DownstreamOpenResponseExtensionFields{},
				})

				mustWrite(t, d.srv, &message.DownstreamChunk{
					StreamIDAlias: 2,
					StreamChunk: &message.StreamChunk{
						SequenceNumber: seq,
						DataPointGroups: []*message.DataPointGroup{
							{
								DataPoints: []*message.DataPoint{
									{
										ElapsedTime: dataPoint.ElapsedTime,
										Payload:     dataPoint.Payload,
									},
								},
								DataIDOrAlias: dataID,
							},
						},
					},
					UpstreamOrAlias: info,
				})

				assert.Equal(t, &message.DownstreamChunkAck{
					StreamIDAlias: 0x2,
					AckID:         0x1,
					Results: []*message.DownstreamChunkResult{
						{
							StreamIDOfUpstream:       info.StreamID,
							SequenceNumberInUpstream: seq,
							ResultCode:               message.ResultCodeSucceeded,
							ResultString:             "OK",
						},
					},
					UpstreamAliases: map[uint32]*message.UpstreamInfo{1: info},
					DataIDAliases: map[uint32]*message.DataID{
						1: dataID,
					},
				}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}))

				closeRequest := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.DownstreamCloseRequest)
				assert.Equal(t, &message.DownstreamCloseRequest{
					RequestID: closeRequest.RequestID,
					StreamID:  uuid.MustParse("11111111-1111-1111-1111-111111111111"),
				}, closeRequest)
				mustWrite(t, d.srv, &message.DownstreamCloseResponse{
					RequestID:    closeRequest.RequestID,
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
				iscp.WithConnNodeID(nodeID),
			)
			require.NoError(t, err)
			defer conn.Close(ctx)
			down, err := conn.OpenDownstream(ctx, []*message.DownstreamFilter{
				{
					SourceNodeID: nodeID,
					DataFilters: []*message.DataFilter{
						{
							Name: "#",
							Type: "#",
						},
					},
				},
			},
				iscp.WithDownstreamQoS(tt.qos),
			)
			require.NoError(t, err)
			defer down.Close(ctx)

			cctx, ccancel := context.WithTimeout(ctx, time.Second)
			defer ccancel()

			got, err := down.ReadDataPoints(cctx)
			assert.Equal(t, want, got)
		})
	}
}

func TestDownstream_ReceiveMetadata(t *testing.T) {
	nodeID := "11111111-1111-1111-1111-111111111111"
	baseTime := &message.BaseTime{
		SessionID:   "session_id",
		Name:        "name",
		Priority:    99,
		ElapsedTime: time.Second,
		BaseTime:    time.Date(2000, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	tests := []struct {
		name      string
		transport TransportName
		qos       message.QoS
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
				downstreamOpenReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.DownstreamOpenRequest)
				assert.Equal(t, &message.DownstreamOpenRequest{
					RequestID:            downstreamOpenReq.RequestID,
					DesiredStreamIDAlias: 2,
					DownstreamFilters: []*message.DownstreamFilter{
						{
							SourceNodeID: nodeID,
							DataFilters: []*message.DataFilter{
								{
									Name: "#",
									Type: "#",
								},
							},
						},
					},
					ExpiryInterval: time.Minute,
					DataIDAliases:  map[uint32]*message.DataID{},
					QoS:            tt.qos,
				}, downstreamOpenReq)
				mustWrite(t, d.srv, &message.DownstreamOpenResponse{
					RequestID:        downstreamOpenReq.RequestID,
					AssignedStreamID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
					ResultCode:       message.ResultCodeSucceeded,
					ResultString:     "OK",
					ExtensionFields:  &message.DownstreamOpenResponseExtensionFields{},
				})

				mustWrite(t, d.srv, &message.DownstreamMetadata{
					RequestID:       3,
					StreamIDAlias:   2,
					SourceNodeID:    nodeID,
					Metadata:        baseTime,
					ExtensionFields: &message.DownstreamMetadataExtensionFields{},
				})
				assert.Equal(t, &message.DownstreamMetadataAck{
					RequestID:    3,
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "OK",
				}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}))

				closeRequest := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}, &message.DownstreamMetadataAck{}).(*message.DownstreamCloseRequest)
				assert.Equal(t, &message.DownstreamCloseRequest{
					RequestID: closeRequest.RequestID,
					StreamID:  uuid.MustParse("11111111-1111-1111-1111-111111111111"),
				}, closeRequest)
				mustWrite(t, d.srv, &message.DownstreamCloseResponse{
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
			down, err := conn.OpenDownstream(ctx,
				[]*message.DownstreamFilter{message.NewDownstreamFilterAllFor(nodeID)},
				iscp.WithDownstreamQoS(tt.qos),
			)
			require.NoError(t, err)
			defer down.Close(ctx)
			ctx, cancel = context.WithTimeout(ctx, time.Second*2)
			defer cancel()

			got, err := down.ReadMetadata(ctx)
			require.NoError(t, err)

			want := &DownstreamMetadata{
				SourceNodeID: nodeID,
				Metadata:     baseTime,
			}
			assert.Equal(t, want, got)
		})
	}
}

func TestDownstream_ClientConnClose(t *testing.T) {
	nodeID := "11111111-1111-1111-1111-111111111111"
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
				msg := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.DownstreamOpenRequest)
				assert.Equal(t, &message.DownstreamOpenRequest{
					RequestID:            msg.RequestID,
					DesiredStreamIDAlias: 2,
					DownstreamFilters: []*message.DownstreamFilter{
						{
							SourceNodeID: nodeID,
							DataFilters: []*message.DataFilter{
								{
									Name: "#",
									Type: "#",
								},
							},
						},
					},
					ExpiryInterval: time.Minute,
					DataIDAliases:  map[uint32]*message.DataID{},
					QoS:            tt.qos,
				}, msg)
				mustWrite(t, d.srv, &message.DownstreamOpenResponse{
					RequestID:        msg.RequestID,
					AssignedStreamID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
					ResultCode:       message.ResultCodeSucceeded,
					ResultString:     "OK",
					ExtensionFields:  &message.DownstreamOpenResponseExtensionFields{},
				})
				assert.Equal(t, &message.Disconnect{
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "NormalClosure",
				}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}))
			}()

			ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
			defer cancel()

			conn, err := Connect("dummy", TransportTest,
				iscp.WithConnNodeID(nodeID),
			)
			require.NoError(t, err)
			down, err := conn.OpenDownstream(ctx,
				[]*message.DownstreamFilter{
					message.NewDownstreamFilterAllFor(nodeID),
				}, iscp.WithDownstreamQoS(tt.qos))
			require.NoError(t, err)
			defer down.Close(ctx)

			require.NoError(t, conn.Close(ctx))

			cctx, ccancel := context.WithTimeout(ctx, time.Second)
			defer ccancel()
			_, err = down.ReadDataPoints(cctx)
			assert.ErrorIs(t, err, errors.ErrStreamClosed)
		})
	}
}

func TestDownstream_ReadDataPointsMulti(t *testing.T) {
	nodeID := "11111111-1111-1111-1111-111111111111"
	dataPointCount := 100
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
				downstreamOpenReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.DownstreamOpenRequest)
				assert.Equal(t, &message.DownstreamOpenRequest{
					RequestID:            downstreamOpenReq.RequestID,
					DesiredStreamIDAlias: downstreamOpenReq.DesiredStreamIDAlias,
					DownstreamFilters: []*message.DownstreamFilter{
						{
							SourceNodeID: nodeID,
							DataFilters: []*message.DataFilter{
								{
									Name: "#",
									Type: "#",
								},
							},
						},
					},
					ExpiryInterval: time.Minute,
					DataIDAliases:  map[uint32]*message.DataID{},
					QoS:            tt.qos,
				}, downstreamOpenReq)
				mustWrite(t, d.srv, &message.DownstreamOpenResponse{
					RequestID:        downstreamOpenReq.RequestID,
					AssignedStreamID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
					ResultCode:       message.ResultCodeSucceeded,
					ResultString:     "OK",
					ExtensionFields:  &message.DownstreamOpenResponseExtensionFields{},
				})

				for i := 0; i < dataPointCount; i++ {
					mustWrite(t, d.srv, &message.DownstreamChunk{
						StreamIDAlias: downstreamOpenReq.DesiredStreamIDAlias,
						StreamChunk: &message.StreamChunk{
							SequenceNumber: uint32(i + 1),
							DataPointGroups: []*message.DataPointGroup{
								{
									DataPoints: []*message.DataPoint{
										{
											ElapsedTime: time.Millisecond * time.Duration(i),
											Payload:     []byte{byte(i)},
										},
									},
									DataIDOrAlias: &message.DataID{
										Name: "1",
										Type: "1",
									},
								},
							},
						},
						UpstreamOrAlias: &message.UpstreamInfo{
							SessionID:    "session_id",
							SourceNodeID: nodeID,
							StreamID:     uuid.MustParse("121b8205-e7cf-4e22-8b23-48d834de8c2c"),
						},
					})
				}
				closeRequest := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}, &message.DownstreamChunkAck{}).(*message.DownstreamCloseRequest)
				assert.Equal(t, &message.DownstreamCloseRequest{
					RequestID: closeRequest.RequestID,
					StreamID:  uuid.MustParse("11111111-1111-1111-1111-111111111111"),
				}, closeRequest)
				mustWrite(t, d.srv, &message.DownstreamCloseResponse{
					RequestID:    closeRequest.RequestID,
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "OK",
				})
				assert.Equal(t, &message.Disconnect{
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "NormalClosure",
				}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}, &message.DownstreamChunkAck{}))
			}()

			ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
			defer cancel()

			conn, err := Connect("dummy", TransportTest,
				iscp.WithConnNodeID(nodeID),
			)
			require.NoError(t, err)
			defer conn.Close(ctx)
			down, err := conn.OpenDownstream(ctx,
				[]*message.DownstreamFilter{message.NewDownstreamFilterAllFor(nodeID)},
				iscp.WithDownstreamQoS(tt.qos),
			)
			require.NoError(t, err)
			defer down.Close(ctx)

			var count int

			for count != dataPointCount {
				dps, err := down.ReadDataPoints(ctx)
				if err != nil {
					break
				}
				count += len(dps.DataPointGroups)
			}
			assert.Equal(t, dataPointCount, count)
		})
	}
}

func TestDownstream_ReceiveDataFromMultiNode(t *testing.T) {
	nodeID1 := "11111111-1111-1111-1111-111111111111"
	nodeID2 := "22222222-2222-2222-2222-222222222222"
	stubDPS := []*message.DataPointGroup{
		{
			DataIDOrAlias: &message.DataID{
				Name: "1",
				Type: "1",
			},
			DataPoints: []*message.DataPoint{
				{
					ElapsedTime: time.Millisecond,
					Payload:     []byte{1},
				},
			},
		},
	}
	tests := []struct {
		name      string
		transport TransportName
		qos       message.QoS
	}{
		{
			name:      "success reliable",
			transport: TransportNameWebSocket,
			qos:       message.QoSReliable,
		},
		{
			name:      "success unreliable",
			transport: TransportNameQUIC,
			qos:       message.QoSUnreliable,
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
				downstreamOpenReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.DownstreamOpenRequest)
				assert.Equal(t, &message.DownstreamOpenRequest{
					RequestID:            downstreamOpenReq.RequestID,
					DesiredStreamIDAlias: 2,
					DownstreamFilters: []*message.DownstreamFilter{
						{
							SourceNodeID: nodeID1,
							DataFilters: []*message.DataFilter{
								{
									Name: "#",
									Type: "#",
								},
							},
						},
						{
							SourceNodeID: nodeID2,
							DataFilters: []*message.DataFilter{
								{
									Name: "#",
									Type: "#",
								},
							},
						},
					},
					ExpiryInterval: time.Minute,
					DataIDAliases:  map[uint32]*message.DataID{},
					QoS:            tt.qos,
				}, downstreamOpenReq)
				mustWrite(t, d.srv, &message.DownstreamOpenResponse{
					RequestID:        downstreamOpenReq.RequestID,
					AssignedStreamID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
					ResultCode:       message.ResultCodeSucceeded,
					ResultString:     "OK",
					ExtensionFields:  &message.DownstreamOpenResponseExtensionFields{},
				})

				mustWrite(t, d.srv, &message.DownstreamChunk{
					StreamIDAlias: 2,
					StreamChunk: &message.StreamChunk{
						SequenceNumber:  1,
						DataPointGroups: stubDPS,
					},
					UpstreamOrAlias: &message.UpstreamInfo{
						SessionID:    "session_id",
						SourceNodeID: nodeID1,
						StreamID:     uuid.MustParse("121b8205-e7cf-4e22-8b23-48d834de8c2c"),
					},
				})
				mustWrite(t, d.srv, &message.DownstreamChunk{
					StreamIDAlias: 2,
					StreamChunk: &message.StreamChunk{
						SequenceNumber:  1,
						DataPointGroups: stubDPS,
					},
					UpstreamOrAlias: &message.UpstreamInfo{
						SessionID:    "session_id",
						SourceNodeID: nodeID2,
						StreamID:     uuid.MustParse("bac25c84-52b5-4921-a9e6-590507349cd5"),
					},
				})

				closeRequest := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}, &message.DownstreamChunkAck{}).(*message.DownstreamCloseRequest)
				assert.Equal(t, &message.DownstreamCloseRequest{
					RequestID: closeRequest.RequestID,
					StreamID:  uuid.MustParse("11111111-1111-1111-1111-111111111111"),
				}, closeRequest)

				mustWrite(t, d.srv, &message.DownstreamCloseResponse{
					RequestID:    closeRequest.RequestID,
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "OK",
				})

				assert.Equal(t, &message.Disconnect{
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "NormalClosure",
				}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}, &message.DownstreamChunkAck{}))
			}()

			ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
			defer cancel()

			conn, err := Connect("dummy ", TransportTest,
				iscp.WithConnNodeID(nodeID1),
			)
			require.NoError(t, err)
			defer conn.Close(ctx)
			down, err := conn.OpenDownstream(ctx,
				[]*message.DownstreamFilter{
					message.NewDownstreamFilterAllFor(nodeID1),
					message.NewDownstreamFilterAllFor(nodeID2),
				},
				iscp.WithDownstreamQoS(tt.qos),
			)
			require.NoError(t, err)
			defer down.Close(ctx)

			cctx, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			var (
				gotNode1 *DownstreamChunk
				gotNode2 *DownstreamChunk
			)
			for i := 0; i < 2; i++ {
				dps, err := down.ReadDataPoints(cctx)
				require.NoError(t, err)
				if dps.UpstreamInfo.SourceNodeID == nodeID1 {
					gotNode1 = dps
				} else if dps.UpstreamInfo.SourceNodeID == nodeID2 {
					gotNode2 = dps
				} else {
					break
				}
			}
			require.NotEmpty(t, gotNode1)
			require.NotEmpty(t, gotNode2)
		})
	}
}

func TestDownstream_Resume(t *testing.T) {
	nodeID := "11111111-1111-1111-1111-111111111111"
	defer goleak.VerifyNone(t)
	ds := []*dialer{newDialer(transport.NegotiationParams{}), newDialer(transport.NegotiationParams{})}
	var callCount int
	RegisterDialer(TransportTest, func() transport.Dialer {
		callCount++
		time.Sleep(time.Duration(callCount) * time.Millisecond)
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
		msg, ok := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.DownstreamOpenRequest)
		require.True(t, ok)
		mustWrite(t, d.srv, &message.DownstreamOpenResponse{
			RequestID:        msg.RequestID,
			AssignedStreamID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			ResultCode:       message.ResultCodeSucceeded,
			ResultString:     "OK",
			ExtensionFields:  &message.DownstreamOpenResponseExtensionFields{},
		})
		t.Log("Server:OpenDownstream")
	}()

	go func() {
		defer close(done)
		d := ds[1]
		mockConnectRequest(t, d.srv)
		t.Log("Server:Reconnected")
		msg := mustRead(t, d.srv, &message.Ping{}, &message.Pong{})
		req, ok := msg.(*message.DownstreamResumeRequest)
		require.True(t, ok, "%T", msg)
		assert.Equal(t, &message.DownstreamResumeRequest{
			RequestID:            req.RequestID,
			StreamID:             uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			DesiredStreamIDAlias: req.DesiredStreamIDAlias,
		}, msg)
		mustWrite(t, d.srv, &message.DownstreamResumeResponse{
			RequestID:       req.RequestID,
			ResultCode:      message.ResultCodeSucceeded,
			ResultString:    "OK",
			ExtensionFields: &message.DownstreamResumeResponseExtensionFields{},
		})
		t.Log("Server:ResumeDownstream")

		mustWrite(t, d.srv, &message.DownstreamChunk{
			StreamIDAlias: req.DesiredStreamIDAlias,
			UpstreamOrAlias: &message.UpstreamInfo{
				SessionID:    "11111111-1111-1111-1111-111111111111",
				SourceNodeID: nodeID,
				StreamID:     uuid.MustParse("22222222-2222-2222-2222-222222222222"),
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
								ElapsedTime: 1,
								Payload:     []byte{0, 1, 2, 3},
							},
						},
					},
				},
			},
			ExtensionFields: &message.DownstreamChunkExtensionFields{},
		})

		mustWrite(t, d.srv, &message.DownstreamMetadata{
			RequestID:     1,
			SourceNodeID:  nodeID,
			StreamIDAlias: req.DesiredStreamIDAlias,
			Metadata: &message.DownstreamOpen{
				StreamID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
				DownstreamFilters: []*message.DownstreamFilter{
					{
						SourceNodeID: nodeID,
						DataFilters: []*message.DataFilter{
							{
								Name: "#",
								Type: "#",
							},
						},
					},
				},
				QoS: message.QoSPartial,
			},
			ExtensionFields: &message.DownstreamMetadataExtensionFields{},
		})

		closeRequest := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}, &message.DownstreamMetadataAck{}, &message.DownstreamChunkAck{}).(*message.DownstreamCloseRequest)
		assert.Equal(t, &message.DownstreamCloseRequest{
			RequestID: closeRequest.RequestID,
			StreamID:  uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		}, closeRequest)

		mustWrite(t, d.srv, &message.DownstreamCloseResponse{
			RequestID:    closeRequest.RequestID,
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "OK",
		})

		assert.Equal(t, &message.Disconnect{
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "NormalClosure",
		}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}, &message.DownstreamChunkAck{}, &message.DownstreamMetadataAck{}))
	}()
	ctx := context.Background()
	conn, err := Connect("dummy", TransportTest,
		iscp.WithConnNodeID(nodeID),
		iscp.WithConnLogger(log.NewStd()),
	)
	require.NoError(t, err)
	defer conn.Close(ctx)

	closedEvCh := make(chan *iscp.DownstreamClosedEvent, 0)
	resumedEvCh := make(chan *DownstreamResumedEvent, 0)
	down, err := conn.OpenDownstream(ctx, []*message.DownstreamFilter{
		{
			SourceNodeID: nodeID,
			DataFilters: []*message.DataFilter{
				{
					Name: "#",
					Type: "#",
				},
			},
		},
	},
		WithDownstreamQoS(message.QoSPartial),
		WithDownstreamExpiryInterval(time.Second*10),
		WithDownstreamClosedEventHandler(DownstreamClosedEventHandlerFunc(func(ev *iscp.DownstreamClosedEvent) {
			closedEvCh <- ev
		})),
		WithDownstreamResumedEventHandler(DownstreamResumedEventHandlerFunc(func(ev *iscp.DownstreamResumedEvent) {
			resumedEvCh <- ev
		})),
	)
	require.NoError(t, err)
	defer down.Close(ctx)
	ds[0].Close()

	gotChunkCh := make(chan *DownstreamChunk, 1)
	go func() {
		gotChunk, err := down.ReadDataPoints(ctx)
		require.NoError(t, err)
		gotChunkCh <- gotChunk
	}()

	gotMetadataCh := make(chan *DownstreamMetadata, 2)
	go func() {
		gotMetadata, err := down.ReadMetadata(ctx)
		require.NoError(t, err)
		gotMetadataCh <- gotMetadata
	}()

	wantChunk := &iscp.DownstreamChunk{
		SequenceNumber: 1,
		DataPointGroups: []*DataPointGroup{
			{
				DataID: &message.DataID{
					Name: "name",
					Type: "type",
				},
				DataPoints: iscp.DataPoints{
					{
						ElapsedTime: 1,
						Payload:     []byte{0, 1, 2, 3},
					},
				},
			},
		},
		UpstreamInfo: &message.UpstreamInfo{
			SessionID:    "11111111-1111-1111-1111-111111111111",
			SourceNodeID: nodeID,
			StreamID:     uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		},
	}
	assert.Equal(t, wantChunk, <-gotChunkCh)

	wantMetadataDownstreamOpen := &iscp.DownstreamMetadata{
		SourceNodeID: nodeID,
		Metadata: &message.DownstreamOpen{
			StreamID: down.ID,
			DownstreamFilters: []*message.DownstreamFilter{
				{
					SourceNodeID: nodeID,
					DataFilters: []*message.DataFilter{
						{
							Name: "#",
							Type: "#",
						},
					},
				},
			},
			QoS: message.QoSPartial,
		},
	}
	assert.Equal(t, wantMetadataDownstreamOpen, <-gotMetadataCh)

	down.Close(ctx)
	gotResumedEvent := <-resumedEvCh
	assert.EqualValues(t, down.ID, gotResumedEvent.ID)

	gotClosedEvent := <-closedEvCh
	assert.Equal(t, down.State(), &gotClosedEvent.State)
}

func TestDownstream_Resume_Failure(t *testing.T) {
	nodeID := "11111111-1111-1111-1111-111111111111"
	defer goleak.VerifyNone(t)
	ds := []*dialer{newDialer(transport.NegotiationParams{}), newDialer(transport.NegotiationParams{})}
	var callCount int
	RegisterDialer(TransportTest, func() transport.Dialer {
		callCount++
		time.Sleep(time.Duration(callCount) * time.Millisecond)
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
		msg, ok := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.DownstreamOpenRequest)
		require.True(t, ok)
		mustWrite(t, d.srv, &message.DownstreamOpenResponse{
			RequestID:        msg.RequestID,
			AssignedStreamID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			ResultCode:       message.ResultCodeSucceeded,
			ResultString:     "OK",
			ExtensionFields:  &message.DownstreamOpenResponseExtensionFields{},
		})
		t.Log("Server:OpenDownstream")
	}()

	go func() {
		defer close(done)
		d := ds[1]
		mockConnectRequest(t, d.srv)
		t.Log("Server:Reconnected")
		msg := mustRead(t, d.srv, &message.Ping{}, &message.Pong{})
		req, ok := msg.(*message.DownstreamResumeRequest)
		require.True(t, ok, "%T", msg)
		assert.Equal(t, &message.DownstreamResumeRequest{
			RequestID:            req.RequestID,
			StreamID:             uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			DesiredStreamIDAlias: req.DesiredStreamIDAlias,
		}, msg)
		mustWrite(t, d.srv, &message.DownstreamResumeResponse{
			RequestID:       req.RequestID,
			ResultCode:      message.ResultCodeStreamNotFound,
			ResultString:    "Not found stream",
			ExtensionFields: &message.DownstreamResumeResponseExtensionFields{},
		})

		closeRequest := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}, &message.DownstreamMetadataAck{}, &message.DownstreamChunkAck{}).(*message.DownstreamCloseRequest)
		assert.Equal(t, &message.DownstreamCloseRequest{
			RequestID: closeRequest.RequestID,
			StreamID:  uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		}, closeRequest)

		mustWrite(t, d.srv, &message.DownstreamCloseResponse{
			RequestID:    closeRequest.RequestID,
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "OK",
		})

		assert.Equal(t, &message.Disconnect{
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "NormalClosure",
		}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}, &message.DownstreamChunkAck{}, &message.DownstreamMetadataAck{}))
	}()
	ctx := context.Background()
	conn, err := Connect("dummy", TransportTest,
		iscp.WithConnNodeID(nodeID),
		iscp.WithConnLogger(log.NewStd()),
	)
	require.NoError(t, err)
	defer conn.Close(ctx)

	closedEvCh := make(chan *iscp.DownstreamClosedEvent, 0)
	down, err := conn.OpenDownstream(ctx, []*message.DownstreamFilter{
		{
			SourceNodeID: nodeID,
			DataFilters: []*message.DataFilter{
				{
					Name: "#",
					Type: "#",
				},
			},
		},
	},
		WithDownstreamQoS(message.QoSPartial),
		WithDownstreamExpiryInterval(time.Second),
		WithDownstreamClosedEventHandler(DownstreamClosedEventHandlerFunc(func(ev *iscp.DownstreamClosedEvent) {
			closedEvCh <- ev
		})),
	)
	require.NoError(t, err)
	defer down.Close(ctx)
	ds[0].Close()

	gotClosedEvent := <-closedEvCh
	assert.Equal(t, down.State(), &gotClosedEvent.State)

	var gotErr *errors.FailedMessageError
	require.ErrorAs(t, gotClosedEvent.Err, &gotErr)
	assert.Equal(t, message.ResultCodeStreamNotFound, gotErr.ResultCode)
}

func TestDownstream_ReceiveMetadata_Multi(t *testing.T) {
	nodeID := "11111111-1111-1111-1111-111111111111"
	baseTime := &message.BaseTime{
		SessionID:   "session_id",
		Name:        "name",
		Priority:    99,
		ElapsedTime: time.Second,
		BaseTime:    time.Date(2000, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	tests := []struct {
		name      string
		transport TransportName
		qos       message.QoS
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
				downstreamOpenReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.DownstreamOpenRequest)
				mustWrite(t, d.srv, &message.DownstreamOpenResponse{
					RequestID:        downstreamOpenReq.RequestID,
					AssignedStreamID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
					ResultCode:       message.ResultCodeSucceeded,
					ResultString:     "OK",
					ExtensionFields:  &message.DownstreamOpenResponseExtensionFields{},
				})
				downstreamOpenReq2 := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.DownstreamOpenRequest)
				mustWrite(t, d.srv, &message.DownstreamOpenResponse{
					RequestID:        downstreamOpenReq2.RequestID,
					AssignedStreamID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
					ResultCode:       message.ResultCodeSucceeded,
					ResultString:     "OK",
					ExtensionFields:  &message.DownstreamOpenResponseExtensionFields{},
				})

				mustWrite(t, d.srv, &message.DownstreamMetadata{
					RequestID:       3,
					StreamIDAlias:   downstreamOpenReq.DesiredStreamIDAlias,
					SourceNodeID:    nodeID,
					Metadata:        baseTime,
					ExtensionFields: &message.DownstreamMetadataExtensionFields{},
				})
				mustWrite(t, d.srv, &message.DownstreamMetadata{
					RequestID:       5,
					StreamIDAlias:   downstreamOpenReq2.DesiredStreamIDAlias,
					SourceNodeID:    nodeID,
					Metadata:        baseTime,
					ExtensionFields: &message.DownstreamMetadataExtensionFields{},
				})
				assert.Equal(t, &message.DownstreamMetadataAck{
					RequestID:    3,
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "OK",
				}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}))
				assert.Equal(t, &message.DownstreamMetadataAck{
					RequestID:    5,
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "OK",
				}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}))

				closeRequest := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.DownstreamCloseRequest)
				mustWrite(t, d.srv, &message.DownstreamCloseResponse{
					RequestID:    closeRequest.RequestID,
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "OK",
				})
				closeRequest2 := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.DownstreamCloseRequest)
				mustWrite(t, d.srv, &message.DownstreamCloseResponse{
					RequestID:    closeRequest2.RequestID,
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "OK",
				})
				// Disconnect
				mustRead(t, d.srv, &message.Ping{}, &message.Pong{})
			}()

			ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
			defer cancel()

			conn, err := Connect("dummy", TransportTest,
				iscp.WithConnNodeID("11111111-1111-1111-1111-111111111111"),
			)
			require.NoError(t, err)
			defer conn.Close(ctx)

			down1, err := conn.OpenDownstream(ctx,
				[]*message.DownstreamFilter{message.NewDownstreamFilterAllFor(nodeID)},
				iscp.WithDownstreamQoS(tt.qos),
			)
			require.NoError(t, err)
			defer down1.Close(ctx)
			down2, err := conn.OpenDownstream(ctx,
				[]*message.DownstreamFilter{message.NewDownstreamFilterAllFor(nodeID)},
				iscp.WithDownstreamQoS(tt.qos),
			)
			require.NoError(t, err)
			defer down2.Close(ctx)

			ctx, cancel = context.WithTimeout(ctx, time.Second*2)
			defer cancel()

			got1, err := down1.ReadMetadata(ctx)
			require.NoError(t, err)

			want1 := &DownstreamMetadata{
				SourceNodeID: nodeID,
				Metadata:     baseTime,
			}
			assert.Equal(t, want1, got1)

			got2, err := down2.ReadMetadata(ctx)
			require.NoError(t, err)

			want2 := &DownstreamMetadata{
				SourceNodeID: nodeID,
				Metadata:     baseTime,
			}
			assert.Equal(t, want2, got2)
		})
	}
}

func TestDownstream_ReadChunk(t *testing.T) {
	nodeID := "11111111-1111-1111-1111-111111111111"
	info := &message.UpstreamInfo{
		SessionID:    "session_id",
		SourceNodeID: nodeID,
		StreamID:     uuid.MustParse("121b8205-e7cf-4e22-8b23-48d834de8c2c"),
	}
	dataID := &message.DataID{
		Name: "test",
		Type: "float64",
	}
	dataPoint := &message.DataPoint{
		ElapsedTime: time.Second,
		Payload:     []byte{1, 2, 3, 4},
	}
	seq := uint32(1)
	want := &DownstreamChunk{
		SequenceNumber: seq,
		DataPointGroups: []*DataPointGroup{
			{
				DataID: dataID,
				DataPoints: DataPoints{
					{
						ElapsedTime: dataPoint.ElapsedTime,
						Payload:     dataPoint.Payload,
					},
				},
			},
		},
		UpstreamInfo: info,
	}
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
			defer func() { <-done }()
			go func() {
				defer close(done)
				mockConnectRequest(t, d.srv)
				openReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.DownstreamOpenRequest)
				mustWrite(t, d.srv, &message.DownstreamOpenResponse{
					RequestID:        openReq.RequestID,
					AssignedStreamID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
					ResultCode:       message.ResultCodeSucceeded,
					ResultString:     "OK",
					ExtensionFields:  &message.DownstreamOpenResponseExtensionFields{},
				})
				mustWrite(t, d.srv, &message.DownstreamChunk{
					StreamIDAlias: openReq.DesiredStreamIDAlias,
					StreamChunk: &message.StreamChunk{
						SequenceNumber: seq,
						DataPointGroups: []*message.DataPointGroup{
							{
								DataPoints:    []*message.DataPoint{dataPoint},
								DataIDOrAlias: dataID,
							},
						},
					},
					UpstreamOrAlias: info,
				})
				// DownstreamChunkAck を無視してCloseRequestを待つ
				closeReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}, &message.DownstreamChunkAck{}).(*message.DownstreamCloseRequest)
				mustWrite(t, d.srv, &message.DownstreamCloseResponse{
					RequestID:    closeReq.RequestID,
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "OK",
				})
				mustRead(t, d.srv, &message.Ping{}, &message.Pong{}) // Disconnect
			}()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			conn, err := Connect("dummy", TransportTest, iscp.WithConnNodeID(nodeID))
			require.NoError(t, err)
			defer conn.Close(ctx)

			down, err := conn.OpenDownstream(ctx,
				[]*message.DownstreamFilter{message.NewDownstreamFilterAllFor(nodeID)},
				iscp.WithDownstreamQoS(tt.qos),
			)
			require.NoError(t, err)
			defer down.Close(ctx)

			readCtx, readCancel := context.WithTimeout(ctx, 2*time.Second)
			defer readCancel()

			got, err := down.ReadChunk(readCtx)
			require.NoError(t, err)
			assert.Equal(t, want, got)
		})
	}
}

func TestDownstreamReader_Read(t *testing.T) {
	defer goleak.VerifyNone(t)

	nodeID := "11111111-1111-1111-1111-111111111111"
	info := &message.UpstreamInfo{
		SessionID:    "session_id",
		SourceNodeID: nodeID,
		StreamID:     uuid.MustParse("121b8205-e7cf-4e22-8b23-48d834de8c2c"),
	}
	dataID := &message.DataID{Name: "sensor", Type: "float64"}
	dataPoint := &message.DataPoint{
		ElapsedTime: time.Second,
		Payload:     []byte{0xAB, 0xCD},
	}

	d := newDialer(transport.NegotiationParams{})
	RegisterDialer(TransportTest, func() transport.Dialer { return d })
	done := make(chan struct{})
	defer func() { <-done }()
	go func() {
		defer close(done)
		mockConnectRequest(t, d.srv)
		openReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.DownstreamOpenRequest)
		mustWrite(t, d.srv, &message.DownstreamOpenResponse{
			RequestID:        openReq.RequestID,
			AssignedStreamID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			ResultCode:       message.ResultCodeSucceeded,
			ResultString:     "OK",
			ExtensionFields:  &message.DownstreamOpenResponseExtensionFields{},
		})
		// DataPointGroups[0] にフィルタインデックス0を関連付けるチャンクを送信
		mustWrite(t, d.srv, &message.DownstreamChunk{
			StreamIDAlias: openReq.DesiredStreamIDAlias,
			StreamChunk: &message.StreamChunk{
				SequenceNumber: 1,
				DataPointGroups: []*message.DataPointGroup{
					{
						DataPoints:    []*message.DataPoint{dataPoint},
						DataIDOrAlias: dataID,
					},
				},
			},
			UpstreamOrAlias: info,
			DownstreamFilterReferences: [][]*message.DownstreamFilterReference{
				{
					{DownstreamFilterIndex: 0, DataFilterIndex: 0},
				},
			},
		})
		closeReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}, &message.DownstreamChunkAck{}).(*message.DownstreamCloseRequest)
		mustWrite(t, d.srv, &message.DownstreamCloseResponse{
			RequestID:    closeReq.RequestID,
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "OK",
		})
		mustRead(t, d.srv, &message.Ping{}, &message.Pong{}) // Disconnect
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := Connect("dummy", TransportTest, iscp.WithConnNodeID(nodeID))
	require.NoError(t, err)
	defer conn.Close(ctx)

	down, err := conn.OpenDownstream(ctx,
		[]*message.DownstreamFilter{message.NewDownstreamFilterAllFor(nodeID)},
	)
	require.NoError(t, err)
	defer down.Close(ctx)

	reader, err := down.NewReader(ctx, 0)
	require.NoError(t, err)
	defer reader.Close()

	readCtx, readCancel := context.WithTimeout(ctx, 2*time.Second)
	defer readCancel()

	got, err := reader.Read(readCtx)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, dataID, got.DataID)
	assert.Equal(t, dataPoint, got.DataPoint)
	assert.Equal(t, info, got.UpstreamInfo)
}

func TestDownstreamReader_InvalidFilterIndex(t *testing.T) {
	defer goleak.VerifyNone(t)

	nodeID := "11111111-1111-1111-1111-111111111111"

	d := newDialer(transport.NegotiationParams{})
	RegisterDialer(TransportTest, func() transport.Dialer { return d })
	done := make(chan struct{})
	defer func() { <-done }()
	go func() {
		defer close(done)
		mockConnectRequest(t, d.srv)
		openReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.DownstreamOpenRequest)
		mustWrite(t, d.srv, &message.DownstreamOpenResponse{
			RequestID:        openReq.RequestID,
			AssignedStreamID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			ResultCode:       message.ResultCodeSucceeded,
			ResultString:     "OK",
			ExtensionFields:  &message.DownstreamOpenResponseExtensionFields{},
		})
		closeReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}, &message.DownstreamChunkAck{}).(*message.DownstreamCloseRequest)
		mustWrite(t, d.srv, &message.DownstreamCloseResponse{
			RequestID:    closeReq.RequestID,
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "OK",
		})
		mustRead(t, d.srv, &message.Ping{}, &message.Pong{}) // Disconnect
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := Connect("dummy", TransportTest, iscp.WithConnNodeID(nodeID))
	require.NoError(t, err)
	defer conn.Close(ctx)

	// フィルタは1つだけ（インデックス0のみ有効）
	down, err := conn.OpenDownstream(ctx,
		[]*message.DownstreamFilter{message.NewDownstreamFilterAllFor(nodeID)},
	)
	require.NoError(t, err)
	defer down.Close(ctx)

	// フィルタインデックス1はアウトオブレンジ（フィルタ数は1）
	_, err = down.NewReader(ctx, 1)
	require.Error(t, err)
}

func TestDownstreamReader_Close(t *testing.T) {
	defer goleak.VerifyNone(t)

	nodeID := "11111111-1111-1111-1111-111111111111"

	d := newDialer(transport.NegotiationParams{})
	RegisterDialer(TransportTest, func() transport.Dialer { return d })
	done := make(chan struct{})
	defer func() { <-done }()
	go func() {
		defer close(done)
		mockConnectRequest(t, d.srv)
		openReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.DownstreamOpenRequest)
		mustWrite(t, d.srv, &message.DownstreamOpenResponse{
			RequestID:        openReq.RequestID,
			AssignedStreamID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			ResultCode:       message.ResultCodeSucceeded,
			ResultString:     "OK",
			ExtensionFields:  &message.DownstreamOpenResponseExtensionFields{},
		})
		closeReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}, &message.DownstreamChunkAck{}).(*message.DownstreamCloseRequest)
		mustWrite(t, d.srv, &message.DownstreamCloseResponse{
			RequestID:    closeReq.RequestID,
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "OK",
		})
		mustRead(t, d.srv, &message.Ping{}, &message.Pong{}) // Disconnect
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := Connect("dummy", TransportTest, iscp.WithConnNodeID(nodeID))
	require.NoError(t, err)
	defer conn.Close(ctx)

	down, err := conn.OpenDownstream(ctx,
		[]*message.DownstreamFilter{message.NewDownstreamFilterAllFor(nodeID)},
	)
	require.NoError(t, err)
	defer down.Close(ctx)

	reader, err := down.NewReader(ctx, 0)
	require.NoError(t, err)

	// Close してから Read するとエラーになること
	err = reader.Close()
	require.NoError(t, err)

	_, err = reader.Read(ctx)
	require.Error(t, err)
}

func TestDownstreamReader_MultipleReaders(t *testing.T) {
	defer goleak.VerifyNone(t)

	nodeID := "11111111-1111-1111-1111-111111111111"
	info := &message.UpstreamInfo{
		SessionID:    "session_id",
		SourceNodeID: nodeID,
		StreamID:     uuid.MustParse("121b8205-e7cf-4e22-8b23-48d834de8c2c"),
	}
	dataID0 := &message.DataID{Name: "sensor0", Type: "float64"}
	dataID1 := &message.DataID{Name: "sensor1", Type: "int32"}
	dataPoint0 := &message.DataPoint{ElapsedTime: time.Second, Payload: []byte{0x01}}
	dataPoint1 := &message.DataPoint{ElapsedTime: 2 * time.Second, Payload: []byte{0x02}}

	d := newDialer(transport.NegotiationParams{})
	RegisterDialer(TransportTest, func() transport.Dialer { return d })
	done := make(chan struct{})
	defer func() { <-done }()
	go func() {
		defer close(done)
		mockConnectRequest(t, d.srv)
		openReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.DownstreamOpenRequest)
		mustWrite(t, d.srv, &message.DownstreamOpenResponse{
			RequestID:        openReq.RequestID,
			AssignedStreamID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			ResultCode:       message.ResultCodeSucceeded,
			ResultString:     "OK",
			ExtensionFields:  &message.DownstreamOpenResponseExtensionFields{},
		})
		// DataPointGroups[0] → filterIdx=0, DataPointGroups[1] → filterIdx=1
		mustWrite(t, d.srv, &message.DownstreamChunk{
			StreamIDAlias: openReq.DesiredStreamIDAlias,
			StreamChunk: &message.StreamChunk{
				SequenceNumber: 1,
				DataPointGroups: []*message.DataPointGroup{
					{DataPoints: []*message.DataPoint{dataPoint0}, DataIDOrAlias: dataID0},
					{DataPoints: []*message.DataPoint{dataPoint1}, DataIDOrAlias: dataID1},
				},
			},
			UpstreamOrAlias: info,
			DownstreamFilterReferences: [][]*message.DownstreamFilterReference{
				{{DownstreamFilterIndex: 0, DataFilterIndex: 0}},
				{{DownstreamFilterIndex: 1, DataFilterIndex: 0}},
			},
		})
		closeReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}, &message.DownstreamChunkAck{}).(*message.DownstreamCloseRequest)
		mustWrite(t, d.srv, &message.DownstreamCloseResponse{
			RequestID:    closeReq.RequestID,
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "OK",
		})
		mustRead(t, d.srv, &message.Ping{}, &message.Pong{}) // Disconnect
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := Connect("dummy", TransportTest, iscp.WithConnNodeID(nodeID))
	require.NoError(t, err)
	defer conn.Close(ctx)

	// 2つのフィルタを持つダウンストリームを開く
	down, err := conn.OpenDownstream(ctx,
		[]*message.DownstreamFilter{
			message.NewDownstreamFilterAllFor(nodeID),
			message.NewDownstreamFilterAllFor(nodeID),
		},
	)
	require.NoError(t, err)
	defer down.Close(ctx)

	reader0, err := down.NewReader(ctx, 0)
	require.NoError(t, err)
	defer reader0.Close()

	reader1, err := down.NewReader(ctx, 1)
	require.NoError(t, err)
	defer reader1.Close()

	readCtx, readCancel := context.WithTimeout(ctx, 2*time.Second)
	defer readCancel()

	// Reader0 はフィルタ0のデータのみ受け取る
	got0, err := reader0.Read(readCtx)
	require.NoError(t, err)
	require.NotNil(t, got0)
	assert.Equal(t, dataID0, got0.DataID)
	assert.Equal(t, dataPoint0, got0.DataPoint)

	// Reader1 はフィルタ1のデータのみ受け取る
	got1, err := reader1.Read(readCtx)
	require.NoError(t, err)
	require.NotNil(t, got1)
	assert.Equal(t, dataID1, got1.DataID)
	assert.Equal(t, dataPoint1, got1.DataPoint)
}

func TestDownstreamReader_SameFilterFanOut(t *testing.T) {
	defer goleak.VerifyNone(t)

	nodeID := "11111111-1111-1111-1111-111111111111"
	info := &message.UpstreamInfo{
		SessionID:    "session_id",
		SourceNodeID: nodeID,
		StreamID:     uuid.MustParse("121b8205-e7cf-4e22-8b23-48d834de8c2c"),
	}
	dataID := &message.DataID{Name: "sensor", Type: "float64"}
	dataPoint := &message.DataPoint{ElapsedTime: time.Second, Payload: []byte{0xAB, 0xCD}}

	d := newDialer(transport.NegotiationParams{})
	RegisterDialer(TransportTest, func() transport.Dialer { return d })
	readerReady := make(chan struct{})
	var readerReadyOnce sync.Once
	signalReaderReady := func() {
		readerReadyOnce.Do(func() { close(readerReady) })
	}
	done := make(chan struct{})
	defer func() {
		signalReaderReady()
		<-done
	}()
	go func() {
		defer close(done)
		mockConnectRequest(t, d.srv)
		openReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.DownstreamOpenRequest)
		mustWrite(t, d.srv, &message.DownstreamOpenResponse{
			RequestID:        openReq.RequestID,
			AssignedStreamID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			ResultCode:       message.ResultCodeSucceeded,
			ResultString:     "OK",
			ExtensionFields:  &message.DownstreamOpenResponseExtensionFields{},
		})
		<-readerReady
		// filterIdx=0 に1つのDataPointGroupを送信
		mustWrite(t, d.srv, &message.DownstreamChunk{
			StreamIDAlias: openReq.DesiredStreamIDAlias,
			StreamChunk: &message.StreamChunk{
				SequenceNumber: 1,
				DataPointGroups: []*message.DataPointGroup{
					{DataPoints: []*message.DataPoint{dataPoint}, DataIDOrAlias: dataID},
				},
			},
			UpstreamOrAlias: info,
			DownstreamFilterReferences: [][]*message.DownstreamFilterReference{
				{{DownstreamFilterIndex: 0, DataFilterIndex: 0}},
			},
		})
		closeReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}, &message.DownstreamChunkAck{}).(*message.DownstreamCloseRequest)
		mustWrite(t, d.srv, &message.DownstreamCloseResponse{
			RequestID:    closeReq.RequestID,
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "OK",
		})
		mustRead(t, d.srv, &message.Ping{}, &message.Pong{}) // Disconnect
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := Connect("dummy", TransportTest, iscp.WithConnNodeID(nodeID))
	require.NoError(t, err)
	defer conn.Close(ctx)

	down, err := conn.OpenDownstream(ctx,
		[]*message.DownstreamFilter{message.NewDownstreamFilterAllFor(nodeID)},
	)
	require.NoError(t, err)
	defer down.Close(ctx)

	// 同じフィルタインデックス0で2つのReaderを作成（fan-out）
	reader0, err := down.NewReader(ctx, 0)
	require.NoError(t, err)
	defer reader0.Close()

	reader1, err := down.NewReader(ctx, 0)
	require.NoError(t, err)
	defer reader1.Close()
	signalReaderReady()

	readCtx, readCancel := context.WithTimeout(ctx, 2*time.Second)
	defer readCancel()

	// 両方のReaderが同じデータを受け取る
	got0, err := reader0.Read(readCtx)
	require.NoError(t, err)
	require.NotNil(t, got0)
	assert.Equal(t, dataID, got0.DataID)
	assert.Equal(t, dataPoint, got0.DataPoint)
	assert.Equal(t, info, got0.UpstreamInfo)

	got1, err := reader1.Read(readCtx)
	require.NoError(t, err)
	require.NotNil(t, got1)
	assert.Equal(t, dataID, got1.DataID)
	assert.Equal(t, dataPoint, got1.DataPoint)
	assert.Equal(t, info, got1.UpstreamInfo)
}

func TestDownstreamReader_WithReadChunk(t *testing.T) {
	defer goleak.VerifyNone(t)

	nodeID := "11111111-1111-1111-1111-111111111111"
	info := &message.UpstreamInfo{
		SessionID:    "session_id",
		SourceNodeID: nodeID,
		StreamID:     uuid.MustParse("121b8205-e7cf-4e22-8b23-48d834de8c2c"),
	}
	dataID0 := &message.DataID{Name: "matched", Type: "float64"}
	dataID1 := &message.DataID{Name: "unmatched", Type: "int32"}
	dataPoint0 := &message.DataPoint{ElapsedTime: time.Second, Payload: []byte{0x01}}
	dataPoint1 := &message.DataPoint{ElapsedTime: 2 * time.Second, Payload: []byte{0x02}}

	d := newDialer(transport.NegotiationParams{})
	RegisterDialer(TransportTest, func() transport.Dialer { return d })
	done := make(chan struct{})
	defer func() { <-done }()
	go func() {
		defer close(done)
		mockConnectRequest(t, d.srv)
		openReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.DownstreamOpenRequest)
		mustWrite(t, d.srv, &message.DownstreamOpenResponse{
			RequestID:        openReq.RequestID,
			AssignedStreamID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			ResultCode:       message.ResultCodeSucceeded,
			ResultString:     "OK",
			ExtensionFields:  &message.DownstreamOpenResponseExtensionFields{},
		})
		// DataPointGroups[0] → filterIdx=0 (Readerが取得)
		// DataPointGroups[1] → filterIdx=1 (ReadChunkが取得)
		mustWrite(t, d.srv, &message.DownstreamChunk{
			StreamIDAlias: openReq.DesiredStreamIDAlias,
			StreamChunk: &message.StreamChunk{
				SequenceNumber: 1,
				DataPointGroups: []*message.DataPointGroup{
					{DataPoints: []*message.DataPoint{dataPoint0}, DataIDOrAlias: dataID0},
					{DataPoints: []*message.DataPoint{dataPoint1}, DataIDOrAlias: dataID1},
				},
			},
			UpstreamOrAlias: info,
			DownstreamFilterReferences: [][]*message.DownstreamFilterReference{
				{{DownstreamFilterIndex: 0, DataFilterIndex: 0}},
				{{DownstreamFilterIndex: 1, DataFilterIndex: 0}},
			},
		})
		closeReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}, &message.DownstreamChunkAck{}).(*message.DownstreamCloseRequest)
		mustWrite(t, d.srv, &message.DownstreamCloseResponse{
			RequestID:    closeReq.RequestID,
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "OK",
		})
		mustRead(t, d.srv, &message.Ping{}, &message.Pong{}) // Disconnect
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := Connect("dummy", TransportTest, iscp.WithConnNodeID(nodeID))
	require.NoError(t, err)
	defer conn.Close(ctx)

	// 2つのフィルタを持つダウンストリームを開く
	down, err := conn.OpenDownstream(ctx,
		[]*message.DownstreamFilter{
			message.NewDownstreamFilterAllFor(nodeID),
			message.NewDownstreamFilterAllFor(nodeID),
		},
	)
	require.NoError(t, err)
	defer down.Close(ctx)

	// filterIdx=0 のみReaderを作成（filterIdx=1はReadChunkで取得）
	reader, err := down.NewReader(ctx, 0)
	require.NoError(t, err)
	defer reader.Close()

	readCtx, readCancel := context.WithTimeout(ctx, 2*time.Second)
	defer readCancel()

	// Reader はフィルタ0のDataPointを受け取る
	gotDp, err := reader.Read(readCtx)
	require.NoError(t, err)
	require.NotNil(t, gotDp)
	assert.Equal(t, dataID0, gotDp.DataID)
	assert.Equal(t, dataPoint0, gotDp.DataPoint)

	// ReadChunk はフィルタ1にマッチしたデータ（unmatchedGroup）を受け取る
	gotChunk, err := down.ReadChunk(readCtx)
	require.NoError(t, err)
	require.NotNil(t, gotChunk)
	require.Len(t, gotChunk.DataPointGroups, 1)
	assert.Equal(t, dataID1, gotChunk.DataPointGroups[0].DataID)
	require.Len(t, gotChunk.DataPointGroups[0].DataPoints, 1)
	assert.Equal(t, dataPoint1, gotChunk.DataPointGroups[0].DataPoints[0])
}

func TestDownstreamReader_Backpressure(t *testing.T) {
	defer goleak.VerifyNone(t)

	nodeID := "11111111-1111-1111-1111-111111111111"
	info := &message.UpstreamInfo{
		SessionID:    "session_id",
		SourceNodeID: nodeID,
		StreamID:     uuid.MustParse("121b8205-e7cf-4e22-8b23-48d834de8c2c"),
	}
	dataID := &message.DataID{Name: "sensor", Type: "float64"}

	const chunkCount = 300 // defaultReaderChBufferSize(256) を超える数

	d := newDialer(transport.NegotiationParams{})
	RegisterDialer(TransportTest, func() transport.Dialer { return d })
	done := make(chan struct{})
	defer func() { <-done }()
	go func() {
		defer close(done)
		mockConnectRequest(t, d.srv)
		openReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.DownstreamOpenRequest)
		mustWrite(t, d.srv, &message.DownstreamOpenResponse{
			RequestID:        openReq.RequestID,
			AssignedStreamID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			ResultCode:       message.ResultCodeSucceeded,
			ResultString:     "OK",
			ExtensionFields:  &message.DownstreamOpenResponseExtensionFields{},
		})
		// バッファを超える数のチャンクを送信する（Readerは読み取らない）
		for i := 0; i < chunkCount; i++ {
			mustWrite(t, d.srv, &message.DownstreamChunk{
				StreamIDAlias: openReq.DesiredStreamIDAlias,
				StreamChunk: &message.StreamChunk{
					SequenceNumber: uint32(i + 1),
					DataPointGroups: []*message.DataPointGroup{
						{
							DataPoints:    []*message.DataPoint{{ElapsedTime: time.Duration(i) * time.Millisecond, Payload: []byte{byte(i % 256)}}},
							DataIDOrAlias: dataID,
						},
					},
				},
				UpstreamOrAlias: info,
				DownstreamFilterReferences: [][]*message.DownstreamFilterReference{
					{{DownstreamFilterIndex: 0, DataFilterIndex: 0}},
				},
			})
		}
		// 全チャンクの送信後にCloseリクエストが届くことを確認（demuxerがブロックしていない）
		closeReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}, &message.DownstreamChunkAck{}).(*message.DownstreamCloseRequest)
		mustWrite(t, d.srv, &message.DownstreamCloseResponse{
			RequestID:    closeReq.RequestID,
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "OK",
		})
		mustRead(t, d.srv, &message.Ping{}, &message.Pong{}) // Disconnect
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := Connect("dummy", TransportTest, iscp.WithConnNodeID(nodeID))
	require.NoError(t, err)
	defer conn.Close(ctx)

	down, err := conn.OpenDownstream(ctx,
		[]*message.DownstreamFilter{message.NewDownstreamFilterAllFor(nodeID)},
	)
	require.NoError(t, err)

	reader, err := down.NewReader(ctx, 0)
	require.NoError(t, err)
	defer reader.Close()

	// Readerは読み取らない状態でDownstreamを閉じる
	// demuxerがブロックせずにCloseが完了することを確認
	closeCtx, closeCancel := context.WithTimeout(ctx, 5*time.Second)
	defer closeCancel()
	err = down.Close(closeCtx)
	require.NoError(t, err)
}

func TestDownstreamReader_StreamClosed(t *testing.T) {
	defer goleak.VerifyNone(t)

	nodeID := "11111111-1111-1111-1111-111111111111"

	d := newDialer(transport.NegotiationParams{})
	RegisterDialer(TransportTest, func() transport.Dialer { return d })
	done := make(chan struct{})
	defer func() { <-done }()
	go func() {
		defer close(done)
		mockConnectRequest(t, d.srv)
		openReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.DownstreamOpenRequest)
		mustWrite(t, d.srv, &message.DownstreamOpenResponse{
			RequestID:        openReq.RequestID,
			AssignedStreamID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			ResultCode:       message.ResultCodeSucceeded,
			ResultString:     "OK",
			ExtensionFields:  &message.DownstreamOpenResponseExtensionFields{},
		})
		closeReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}, &message.DownstreamChunkAck{}).(*message.DownstreamCloseRequest)
		mustWrite(t, d.srv, &message.DownstreamCloseResponse{
			RequestID:    closeReq.RequestID,
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "OK",
		})
		mustRead(t, d.srv, &message.Ping{}, &message.Pong{}) // Disconnect
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := Connect("dummy", TransportTest, iscp.WithConnNodeID(nodeID))
	require.NoError(t, err)
	defer conn.Close(ctx)

	down, err := conn.OpenDownstream(ctx,
		[]*message.DownstreamFilter{message.NewDownstreamFilterAllFor(nodeID)},
	)
	require.NoError(t, err)

	reader, err := down.NewReader(ctx, 0)
	require.NoError(t, err)
	defer reader.Close()

	// Downstream を閉じる
	closeCtx, closeCancel := context.WithTimeout(ctx, 5*time.Second)
	defer closeCancel()
	err = down.Close(closeCtx)
	require.NoError(t, err)

	// Close後にRead するとErrStreamClosedが返る
	readCtx, readCancel := context.WithTimeout(ctx, 2*time.Second)
	defer readCancel()
	_, err = reader.Read(readCtx)
	assert.ErrorIs(t, err, errors.ErrStreamClosed)
}
