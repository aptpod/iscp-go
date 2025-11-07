package iscp_test

import (
	"context"
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
		iscp.WithConnPingInterval(time.Second),
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
	ds[0].srv.Close()

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
		iscp.WithConnPingInterval(time.Second),
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
	ds[0].srv.Close()

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
