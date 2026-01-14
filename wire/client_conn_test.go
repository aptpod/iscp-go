package wire_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	uuid "github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/aptpod/iscp-go/message"
	. "github.com/aptpod/iscp-go/wire"
)

func TestClientConn_Auth(t *testing.T) {
	defer goleak.VerifyNone(t)
	SetDefaultPingTimeout(t, time.Millisecond)

	cli, srv := Pipe()
	go func() {
		mockConnectRequest(t, srv)
	}()

	cliConn, err := Connect(&ClientConnConfig{
		Transport:       cli,
		ProtocolVersion: "2.0.0",
		NodeID:          "11111111-1111-1111-1111-111111111111",
		AccessToken:     "token",
		IntdashExtensionFields: &IntdashExtensionFields{
			ProjectUUID: uuid.MustParse("2b9d6fdc-0318-4db6-b7ce-adba7c059826"),
		},
	})
	require.NoError(t, err)
	defer cliConn.Close()
	select {
	case <-cliConn.Done():
		t.Fatalf("cannot send ping")
	case <-time.After(time.Millisecond):
		return
	}
}

func mockConnectRequest(t *testing.T, srv EncodingTransport) {
	msg, err := srv.Read()
	require.NoError(t, err)
	t.Log(msg)
	require.NoError(t, srv.Write(&message.ConnectResponse{
		RequestID:       0,
		ProtocolVersion: "2.1.0",
		ResultCode:      message.ResultCodeSucceeded,
		ResultString:    "",
		ExtensionFields: &message.ConnectResponseExtensionFields{},
	}))
}

func TestClientConn_keepAlive_timeout(t *testing.T) {
	defer goleak.VerifyNone(t)

	SetDefaultPingInterval(t, time.Millisecond)
	SetDefaultPingTimeout(t, time.Millisecond)

	cliConn, srv := connect(t, nil)
	defer cliConn.Close()

	// confirm connected
	select {
	case <-cliConn.Done():
		t.Fatalf("cannot send ping")
	case <-time.After(time.Millisecond):
		// ok
	}
	require.NoError(t, srv.Close())

	// confirm detect disconnect
	select {
	case <-cliConn.Done():
		// ok
	case <-time.After(time.Millisecond * 100):
		t.Fatalf("cannot detect disconnect")
	}
}

func TestClientConn_SendUpstreamOpenRequest(t *testing.T) {
	dataID := &message.DataID{Name: "name", Type: "type"}
	defer goleak.VerifyNone(t)
	tests := []struct {
		name string
		qoS  message.QoS
	}{
		{
			name: "reliable",
			qoS:  message.QoSReliable,
		},
		{
			name: "unreliable",
			qoS:  message.QoSUnreliable,
		},
		{
			name: "partial",
			qoS:  message.QoSPartial,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			done := make(chan struct{}, 0)
			defer func() {
				<-done
			}()
			cliConn, srv := connect(t, nil)
			defer cliConn.Close()
			go func() {
				defer close(done)
				// Open
				openRequest := mustRead(t, srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamOpenRequest)
				assert.Equal(t, &message.UpstreamOpenRequest{
					RequestID: openRequest.RequestID,
					QoS:       tt.qoS,
					DataIDs:   []*message.DataID{},
				}, openRequest)
				mustWrite(t, srv, &message.UpstreamOpenResponse{
					RequestID:             openRequest.RequestID,
					ResultCode:            message.ResultCodeSucceeded,
					AssignedStreamID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
					AssignedStreamIDAlias: 1,
				})
				// Chunk
				assert.Equal(t, &message.UpstreamChunk{
					StreamIDAlias: 1,
					DataIDs:       []*message.DataID{},
					StreamChunk: &message.StreamChunk{
						SequenceNumber: 1,
						DataPointGroups: []*message.UpstreamDataPointGroup{
							{
								DataIDOrAlias: dataID,
								DataPoints: []*message.DataPoint{
									{
										ElapsedTime: time.Second * 1,
										Payload:     []byte{1},
									},
								},
							},
						},
					},
				}, mustRead(t, srv, &message.Ping{}, &message.Pong{}))

				mustWrite(t, srv, &message.UpstreamChunkAck{
					StreamIDAlias: 1,
					Results: []*message.UpstreamChunkResult{
						{
							SequenceNumber: 1,
							ResultCode:     message.ResultCodeSucceeded,
							ResultString:   "OK",
						},
					},
					DataIDAliases:   map[uint32]*message.DataID{},
					ExtensionFields: &message.UpstreamChunkAckExtensionFields{},
				})

				// Close
				closeRequest := mustRead(t, srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamCloseRequest)
				assert.Equal(t, &message.UpstreamCloseRequest{
					RequestID:           closeRequest.RequestID,
					StreamID:            uuid.MustParse("11111111-1111-1111-1111-111111111111"),
					TotalDataPoints:     1,
					FinalSequenceNumber: 1,
				}, closeRequest)
				mustWrite(t, srv, &message.UpstreamCloseResponse{
					RequestID:    closeRequest.RequestID,
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "OK",
				})
			}()
			ctx := context.Background()
			res, err := cliConn.SendUpstreamOpenRequest(ctx, &message.UpstreamOpenRequest{
				QoS: tt.qoS,
			})
			require.NoError(t, err)
			require.Equal(t, message.ResultCodeSucceeded, res.ResultCode)
			require.EqualValues(t, 1, res.AssignedStreamIDAlias)

			ackCh, err := cliConn.SubscribeUpstreamChunkAck(ctx, res.AssignedStreamIDAlias)
			require.NoError(t, err)

			err = cliConn.SendUpstreamChunk(ctx, &message.UpstreamChunk{
				StreamIDAlias: res.AssignedStreamIDAlias,
				StreamChunk: &message.StreamChunk{
					SequenceNumber: 1,
					DataPointGroups: []*message.UpstreamDataPointGroup{
						{
							DataIDOrAlias: dataID,
							DataPoints: []*message.DataPoint{
								{
									ElapsedTime: time.Second * 1,
									Payload:     []byte{1},
								},
							},
						},
					},
				},
			})
			require.NoError(t, err)

			// confirm ack
			cctx, cancel := context.WithTimeout(ctx, time.Millisecond*100)
			defer cancel()
			select {
			case <-cctx.Done():
				t.Fatalf("timeout")
			case ack := <-ackCh:
				assert.Equal(t, res.AssignedStreamIDAlias, ack.StreamIDAlias)

				for i, v := range ack.Results {
					assert.Equal(t, &message.UpstreamChunkResult{
						SequenceNumber: uint32(i + 1),
						ResultCode:     message.ResultCodeSucceeded,
						ResultString:   "OK",
					}, v)
				}
			}

			resp, err := cliConn.SendUpstreamCloseRequest(ctx, &message.UpstreamCloseRequest{
				StreamID:            res.AssignedStreamID,
				TotalDataPoints:     1,
				FinalSequenceNumber: 1,
			})

			assert.NoError(t, err)
			assert.Equal(t, message.ResultCodeSucceeded, resp.ResultCode)
		})
	}
}

func TestClientConn_SendDownstreamOpenRequest(t *testing.T) {
	sourceNodeID := "source_node_id"
	dataID := &message.DataID{Name: "name", Type: "type"}
	testSessionID := "session_id"
	upstreamInfo := &message.UpstreamInfo{
		SessionID:    testSessionID,
		SourceNodeID: sourceNodeID,
		StreamID:     uuid.MustParse("11111111-1111-1111-1111-111111111111"),
	}
	dataPoints := []*message.DataPoint{
		{
			ElapsedTime: time.Second * 1,
			Payload:     []byte{1},
		},
	}
	baseTime := &message.BaseTime{
		SessionID:   testSessionID,
		Name:        "test",
		Priority:    99,
		ElapsedTime: time.Second,
		BaseTime:    time.Date(2000, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	defer goleak.VerifyNone(t)
	tests := []struct {
		name string
		qoS  message.QoS
	}{
		{
			name: "reliable",
			qoS:  message.QoSReliable,
		},
		{
			name: "unreliable",
			qoS:  message.QoSUnreliable,
		},
		{
			name: "partial",
			qoS:  message.QoSPartial,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			done := make(chan struct{}, 0)
			defer func() {
				<-done
			}()
			cliConn, srv := connect(t, nil)
			defer cliConn.Close()
			go func() {
				defer close(done)
				// Open
				openRequest := mustRead(t, srv, &message.Ping{}, &message.Pong{}).(*message.DownstreamOpenRequest)
				assert.Equal(t, &message.DownstreamOpenRequest{
					RequestID:            openRequest.RequestID,
					DesiredStreamIDAlias: 1,
					QoS:                  tt.qoS,
					DataIDAliases:        map[uint32]*message.DataID{},
					DownstreamFilters: []*message.DownstreamFilter{
						{
							SourceNodeID: sourceNodeID,
							DataFilters: []*message.DataFilter{
								{
									Name: "#",
									Type: "#",
								},
							},
						},
					},
				}, openRequest)
				mustWrite(t, srv, &message.DownstreamOpenResponse{
					RequestID:        openRequest.RequestID,
					ResultCode:       message.ResultCodeSucceeded,
					AssignedStreamID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
				})
				// Chunk
				mustWrite(t, srv, &message.DownstreamChunk{
					StreamIDAlias:   1,
					UpstreamOrAlias: upstreamInfo,
					StreamChunk: &message.StreamChunk{
						SequenceNumber: 1,
						DataPointGroups: []*message.DownstreamDataPointGroup{
							{
								DataIDOrAlias: dataID,
								DataPoints:    dataPoints,
							},
						},
					},
				})

				assert.Equal(t, &message.DownstreamChunkAck{
					StreamIDAlias: 1,
					AckID:         0,
					Results:       []*message.DownstreamChunkResult{},
					UpstreamAliases: map[uint32]*message.UpstreamInfo{
						1: upstreamInfo,
					},
					DataIDAliases: map[uint32]*message.DataID{
						1: dataID,
					},
				}, mustRead(t, srv, &message.Ping{}, &message.Pong{}))

				mustWrite(t, srv, &message.DownstreamChunkAckComplete{
					StreamIDAlias: 1,
					AckID:         0,
					ResultCode:    message.ResultCodeSucceeded,
					ResultString:  "OK",
				})

				mustWrite(t, srv, &message.DownstreamMetadata{
					RequestID:     3,
					StreamIDAlias: 1,
					SourceNodeID:  sourceNodeID,
					Metadata:      baseTime,
				})
				assert.Equal(t, &message.DownstreamMetadataAck{
					RequestID:    3,
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "OK",
				}, mustRead(t, srv, &message.Ping{}, &message.Pong{}))
				// Close
				closeRequest := mustRead(t, srv, &message.Ping{}, &message.Pong{}, &message.DownstreamMetadataAck{}).(*message.DownstreamCloseRequest)
				assert.Equal(t, &message.DownstreamCloseRequest{
					RequestID: closeRequest.RequestID,
					StreamID:  uuid.MustParse("11111111-1111-1111-1111-111111111111"),
				}, closeRequest)
				mustWrite(t, srv, &message.DownstreamCloseResponse{
					RequestID:    closeRequest.RequestID,
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "OK",
				})
			}()

			ctx := context.Background()

			dpsCh, err := cliConn.SubscribeDownstreamChunk(ctx, 1, tt.qoS)
			assert.NoError(t, err)
			ackCompleteCh, err := cliConn.SubscribeDownstreamChunkAckComplete(ctx, 1)
			assert.NoError(t, err)

			metaCh, err := cliConn.SubscribeDownstreamMeta(ctx, 1, sourceNodeID)
			assert.NoError(t, err)

			res, err := cliConn.SendDownstreamOpenRequest(ctx, &message.DownstreamOpenRequest{
				DesiredStreamIDAlias: 1,
				QoS:                  tt.qoS,
				DownstreamFilters: []*message.DownstreamFilter{
					{
						SourceNodeID: sourceNodeID,
						DataFilters: []*message.DataFilter{
							{
								Name: "#",
								Type: "#",
							},
						},
					},
				},
			})
			require.NoError(t, err)
			require.Equal(t, message.ResultCodeSucceeded, res.ResultCode)

			select {
			case got := <-dpsCh:
				want := &message.DownstreamChunk{
					StreamIDAlias:   1,
					UpstreamOrAlias: upstreamInfo,
					StreamChunk: &message.StreamChunk{
						SequenceNumber: 1,
						DataPointGroups: []*message.DownstreamDataPointGroup{
							{
								DataIDOrAlias: dataID,
								DataPoints:    dataPoints,
							},
						},
					},
				}
				assert.Equal(t, want, got)

			case <-time.After(time.Second):
				t.Fatalf("timeout")
			}

			// confirm dispatching ack complete
			err = cliConn.SendDownstreamDataPointsAck(ctx, &message.DownstreamChunkAck{
				StreamIDAlias: 1,
				AckID:         0,
				Results:       nil,
				UpstreamAliases: map[uint32]*message.UpstreamInfo{
					1: upstreamInfo,
				},
				DataIDAliases: map[uint32]*message.DataID{
					1: dataID,
				},
			})
			require.NoError(t, err)

			select {
			case got := <-ackCompleteCh:
				want := &message.DownstreamChunkAckComplete{
					StreamIDAlias: 1,
					AckID:         0,
					ResultCode:    message.ResultCodeSucceeded,
					ResultString:  "OK",
				}
				assert.Equal(t, want, got)
			case <-time.After(time.Second):
				t.Fatalf("timeout")
			}

			// metadata
			baseTime := &message.BaseTime{
				SessionID:   testSessionID,
				Name:        "test",
				Priority:    99,
				ElapsedTime: time.Second,
				BaseTime:    time.Date(2000, 1, 2, 3, 4, 5, 0, time.UTC),
			}
			select {
			case meta := <-metaCh:
				want := &message.DownstreamMetadata{
					RequestID:     meta.RequestID,
					StreamIDAlias: meta.StreamIDAlias,
					SourceNodeID:  sourceNodeID,
					Metadata:      baseTime,
				}
				assert.Equal(t, want, meta)
				// confirm dispatching ack complete
				err = cliConn.SendDownstreamMetadataAck(ctx, &message.DownstreamMetadataAck{
					RequestID:    3,
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "OK",
				})
				require.NoError(t, err)
			case <-time.After(time.Second * 3):
				t.Fatalf("timeout")
			}

			resp, err := cliConn.SendDownstreamCloseRequest(ctx, &message.DownstreamCloseRequest{
				StreamID: res.AssignedStreamID,
			})
			require.NoError(t, err)
			require.Equal(t, message.ResultCodeSucceeded, resp.ResultCode)
		})
	}
}

func TestClientConn_UpDownE2E(t *testing.T) {
	defer goleak.VerifyNone(t)
	myNodeID := "11111111-1111-1111-1111-111111111111"
	upstreamCall := &message.UpstreamCall{
		CallID:            "call_id",
		RequestCallID:     "request_call_id",
		DestinationNodeID: myNodeID,
		Name:              "name",
		Type:              "type",
		Payload:           []byte{1, 2, 3, 4},
	}
	downstreamCall := &message.DownstreamCall{
		CallID:        "call_id",
		RequestCallID: "request_call_id",
		SourceNodeID:  myNodeID,
		Name:          "name",
		Type:          "type",
		Payload:       []byte{1, 2, 3, 4},
	}
	done := make(chan struct{}, 0)
	defer func() {
		<-done
	}()
	cliConn, srv := connect(t, nil)
	defer cliConn.Close()
	go func() {
		defer close(done)
		assert.Equal(t, upstreamCall, mustRead(t, srv, &message.Ping{}, &message.Pong{}))
		mustWrite(t, srv, &message.UpstreamCallAck{
			CallID:       "call_id",
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "OK",
		})
		mustWrite(t, srv, downstreamCall)
	}()
	ctx := context.Background()

	err := cliConn.SendUpstreamCall(ctx, upstreamCall)
	require.NoError(t, err)
	ack, err := cliConn.ReceiveUpstreamCallAck(ctx)
	require.NoError(t, err)
	require.Equal(t, message.ResultCodeSucceeded, ack.ResultCode)

	cctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	got, err := cliConn.ReceiveDownstreamCall(cctx)
	require.NoError(t, err)
	assert.Equal(t, downstreamCall, got)
}

func connect(t *testing.T, modifier func(*ClientConnConfig)) (*ClientConn, EncodingTransport) {
	cli, srv := Pipe()
	t.Helper()
	c := &ClientConnConfig{
		Transport:       cli,
		ProtocolVersion: "2.0.0",
		NodeID:          "11111111-1111-1111-1111-111111111111",
	}
	if modifier != nil {
		modifier(c)
	}
	go func() {
		mockConnectRequest(t, srv)
	}()
	cliConn, err := Connect(c)
	require.NoError(t, err)
	return cliConn, srv
}

func mustRead(t *testing.T, tr EncodingTransport, ignores ...message.Message) message.Message {
	for {
		msg, err := tr.Read()
		require.NoError(t, err)
		var ignore bool
		for _, v := range ignores {
			if fmt.Sprintf("%T", msg) == fmt.Sprintf("%T", v) {
				ignore = true
				break
			}
		}
		if ignore {
			continue
		}
		return msg
	}
}

func mustWrite(t *testing.T, tr EncodingTransport, msg message.Message) {
	require.NoError(t, tr.Write(msg))
}
