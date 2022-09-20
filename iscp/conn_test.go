package iscp_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aptpod/iscp-go/iscp"
	. "github.com/aptpod/iscp-go/iscp"
	"github.com/aptpod/iscp-go/log"
	"github.com/aptpod/iscp-go/message"
	"github.com/aptpod/iscp-go/transport"
	"github.com/aptpod/iscp-go/wire"
	"github.com/golang/mock/gomock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestConn_Connect(t *testing.T) {
	defer goleak.VerifyNone(t)
	d1 := newDialer(transport.NegotiationParams{Encoding: transport.EncodingJSON})
	RegisterDialer(TransportTest, func() transport.Dialer { return d1 })
	done := make(chan struct{}, 0)
	defer func() {
		<-done
	}()
	srv := d1.srv
	go func() {
		defer close(done)
		mockConnectRequest(t, srv)
		assert.Equal(t, &message.Disconnect{
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "NormalClosure",
		}, mustRead(t, srv, &message.Ping{}, &message.Pong{}))
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	address := "address"
	conn, err := Connect(address, TransportTest, iscp.WithConnEncoding(iscp.EncodingJSON))
	require.NoError(t, err)
	defer conn.Close(ctx)

	want := iscp.DefaultConnConfig()
	want.Address = address
	want.Transport = TransportTest
	want.Encoding = EncodingJSON
	got := conn.Config
	assert.Equal(t, want, &got)
	assert.NotEqual(t, want, iscp.DefaultConnConfig())
	conn.Close(ctx)

	d2 := newDialer(transport.NegotiationParams{Encoding: transport.EncodingJSON})
	RegisterDialer(TransportTest, func() transport.Dialer { return d2 })
	srv2 := d2.srv
	go func() {
		mockConnectRequest(t, srv2)
		mustRead(t, srv2)
	}()

	conn2, err := iscp.ConnectWithConfig(&got)
	require.NoError(t, err)
	defer conn2.Close(ctx)
	assert.Equal(t, got, conn2.Config)
}

func TestConn_OpenUpstream(t *testing.T) {
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
			AckInterval:     time.Millisecond * 100,
			ExpiryInterval:  time.Second * 10,
			DataIDs:         []*message.DataID{},
			QoS:             message.QoSUnreliable,
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
			TotalDataPoints:     0,
			FinalSequenceNumber: 0,
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

	up, err := conn.OpenUpstream(ctx, "session_id", iscp.WithUpstreamCloseTimeout(time.Second))
	require.NoError(t, err)
	require.NoError(t, up.Close(ctx))
}

func TestConn_OpenDownstream(t *testing.T) {
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
					SourceNodeID: "22222222-2222-2222-2222-222222222222",
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
			QoS:            message.QoSUnreliable,
		}, downstreamOpenReq)
		mustWrite(t, d.srv, &message.DownstreamOpenResponse{
			RequestID:        downstreamOpenReq.RequestID,
			AssignedStreamID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			ResultCode:       message.ResultCodeSucceeded,
			ResultString:     "OK",
			ExtensionFields:  &message.DownstreamOpenResponseExtensionFields{},
		})
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

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	conn, err := Connect("dummy", TransportTest,
		iscp.WithConnNodeID("11111111-1111-1111-1111-111111111111"),
	)
	require.NoError(t, err)
	defer conn.Close(ctx)

	down, err := conn.OpenDownstream(ctx, []*message.DownstreamFilter{
		{
			SourceNodeID: "22222222-2222-2222-2222-222222222222",
			DataFilters: []*message.DataFilter{
				{
					Name: "#",
					Type: "#",
				},
			},
		},
	})
	require.NoError(t, err)
	require.NoError(t, down.Close(ctx))
}

func TestConn_SendBaseTime(t *testing.T) {
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
		metadata := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamMetadata)
		assert.Equal(t, &message.UpstreamMetadata{
			RequestID: metadata.RequestID,
			Metadata: &message.BaseTime{
				SessionID:   "session_id",
				Name:        "name",
				Priority:    99,
				ElapsedTime: time.Second,
				BaseTime:    time.Date(2000, 1, 2, 3, 4, 5, 0, time.UTC),
			},
			ExtensionFields: &message.UpstreamMetadataExtensionFields{
				Persist: false,
			},
		}, metadata)
		mustWrite(t, d.srv, &message.UpstreamMetadataAck{
			RequestID:       metadata.RequestID,
			ResultCode:      message.ResultCodeSucceeded,
			ResultString:    "OK",
			ExtensionFields: &message.UpstreamMetadataAckExtensionFields{},
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

	err = conn.SendBaseTime(ctx, &message.BaseTime{
		SessionID:   "session_id",
		Name:        "name",
		Priority:    99,
		ElapsedTime: time.Second,
		BaseTime:    time.Date(2000, 1, 2, 3, 4, 5, 0, time.UTC),
	})
	assert.NoError(t, err)
}

func Test_Conn_Reconnect(t *testing.T) {
	defer goleak.VerifyNone(t)
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	ctx := context.Background()

	// setup
	nodeID := "11111111-1111-1111-1111-111111111111"

	ds := []*dialer{newDialer(transport.NegotiationParams{}), newDialer(transport.NegotiationParams{})}
	var callCount int
	RegisterDialer(TransportTest,
		func() transport.Dialer {
			callCount++
			time.Sleep(time.Duration(callCount) * time.Millisecond)
			return ds[callCount-1]
		},
	)
	go func() {
		mockConnectRequest(t, ds[0].srv)
	}()

	go func() {
		mockConnectRequest(t, ds[1].srv)

		// Upstream
		umsg := mustRead(t, ds[1].srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamOpenRequest)
		mustWrite(t, ds[1].srv, &message.UpstreamOpenResponse{
			RequestID:  umsg.RequestID,
			ResultCode: message.ResultCodeSucceeded,
		})

		// Downstream
		dmsg := mustRead(t, ds[1].srv, &message.Ping{}, &message.Pong{}).(*message.DownstreamOpenRequest)
		mustWrite(t, ds[1].srv, &message.DownstreamOpenResponse{
			RequestID:  dmsg.RequestID,
			ResultCode: message.ResultCodeSucceeded,
		})
		assert.Equal(t, &message.Disconnect{
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "NormalClosure",
		}, mustRead(t, ds[1].srv, &message.Ping{}, &message.Pong{}))
	}()

	var callReconnected atomic.Bool
	var callDisconnected atomic.Bool
	conn, err := Connect("dummy", TransportTest,
		WithConnNodeID(nodeID),
		WithConnPingInterval(time.Second*2),
		WithConnLogger(log.NewStd()),
		WithConnReconnectedEventHandler(iscp.ReconnectedEventHandlerFunc(func(ev *iscp.ReconnectedEvent) {
			callReconnected.Store(true)
		})),
		WithConnDisconnectedEventHandler(iscp.DisconnectedEventHandlerFunc(func(ev *iscp.DisconnectedEvent) {
			callDisconnected.Store(true)
		})),
	)
	require.NoError(t, err)
	defer conn.Close(ctx)
	ds[0].srv.Close()

	t.Run("OpenUpstream", func(t *testing.T) {
		got, err := conn.OpenUpstream(ctx, "session_id")
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.True(t, callReconnected.Load())
		assert.True(t, callDisconnected.Load())
	})
	t.Run("OpenDownstream", func(t *testing.T) {
		got, err := conn.OpenDownstream(ctx, []*message.DownstreamFilter{})
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.True(t, callReconnected.Load())
		assert.True(t, callDisconnected.Load())
	})
}

func startEchoServer(t *testing.T) wire.Transport {
	srv, cli := Pipe()
	go func() {
		for {
			Copy(srv, cli)
		}
	}()
	return cli
}
