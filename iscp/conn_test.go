package iscp_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AlekSi/pointer"
	"github.com/golang/mock/gomock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/aptpod/iscp-go/v2/iscp"
	. "github.com/aptpod/iscp-go/v2/iscp"
	"github.com/aptpod/iscp-go/v2/log"
	"github.com/aptpod/iscp-go/v2/message"
	"github.com/aptpod/iscp-go/v2/transport"
	"github.com/aptpod/iscp-go/v2/transport/compress"
)

func TestConn_Connect(t *testing.T) {
	t.Run("JSON", func(t *testing.T) {
		testConn_Connect(t, transport.NegotiationParams{Encoding: transport.EncodingNameJSON})
	})
	t.Run("JSON-Compression", func(t *testing.T) {
		testConn_Connect(t, transport.NegotiationParams{
			Encoding:      transport.EncodingNameJSON,
			Compress:      compress.TypePerMessage,
			CompressLevel: pointer.To(6),
		})
	})
}

func testConn_Connect(t *testing.T, params transport.NegotiationParams) {
	defer goleak.VerifyNone(t)
	d1 := newDialer(params)
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
	conn, err := Connect(address, TransportTest, iscp.WithConnEncoding(iscp.EncodingNameJSON))
	require.NoError(t, err)
	defer conn.Close(ctx)

	want := iscp.DefaultConnConfig()
	want.Address = address
	want.Transport = TransportTest
	want.Encoding = EncodingNameJSON
	got := conn.Config
	AssertEQConfig(t, want, &got)
	AssertNotEQConfig(t, want, iscp.DefaultConnConfig())
	conn.Close(ctx)

	d2 := newDialer(transport.NegotiationParams{Encoding: transport.EncodingNameJSON})
	RegisterDialer(TransportTest, func() transport.Dialer { return d2 })
	srv2 := d2.srv
	go func() {
		mockConnectRequest(t, srv2)
		mustRead(t, srv2, &message.Ping{}, &message.Pong{})
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

func TestConn_Connect_MultipleTransport(t *testing.T) {
	defer goleak.VerifyNone(t)
	d1 := newDialer(transport.NegotiationParams{Encoding: transport.EncodingNameJSON})
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

	conn, err := Connect(address, TransportTest, iscp.WithConnEncoding(iscp.EncodingNameJSON), iscp.WithConnMultiTransport(&MultiTransportConfig{
		DialerMap: map[transport.SubConnectionID]transport.Dialer{
			"transport-1": d1,
		},
	}))
	if !assert.NoError(t, err) {
		close(done)
		return
	}
	defer conn.Close(ctx)
}

// TestConn_OpenUpstream_V4 は、プロトコルバージョン4.0.0（Ping/Pong無し）でのUpstreamOpen/Closeをテストします。
func TestConn_OpenUpstream_V4(t *testing.T) {
	defer goleak.VerifyNone(t)
	d := newDialer(transport.NegotiationParams{})
	RegisterDialer(TransportTest, func() transport.Dialer { return d })
	done := make(chan struct{}, 0)
	defer func() {
		<-done
	}()
	go func() {
		defer close(done)
		mockConnectRequestV4(t, d.srv)
		upstreamOpenReq := mustRead(t, d.srv).(*message.UpstreamOpenRequest)
		mustWrite(t, d.srv, &message.UpstreamOpenResponse{
			RequestID:             upstreamOpenReq.RequestID,
			AssignedStreamID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			AssignedStreamIDAlias: 1,
			ResultCode:            message.ResultCodeSucceeded,
			ResultString:          "OK",
			DataIDAliases:         map[uint32]*message.DataID{},
		})
		closeRequest := mustRead(t, d.srv).(*message.UpstreamCloseRequest)
		mustWrite(t, d.srv, &message.UpstreamCloseResponse{
			RequestID:    closeRequest.RequestID,
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "OK",
		})
		assert.Equal(t, &message.Disconnect{
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "NormalClosure",
		}, mustRead(t, d.srv))
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

// TestConn_OpenDownstream_V4 は、プロトコルバージョン4.0.0（Ping/Pong無し）でのDownstreamOpen/Closeをテストします。
func TestConn_OpenDownstream_V4(t *testing.T) {
	defer goleak.VerifyNone(t)
	d := newDialer(transport.NegotiationParams{})
	RegisterDialer(TransportTest, func() transport.Dialer { return d })
	done := make(chan struct{}, 0)
	defer func() {
		<-done
	}()
	go func() {
		defer close(done)
		mockConnectRequestV4(t, d.srv)
		downstreamOpenReq := mustRead(t, d.srv).(*message.DownstreamOpenRequest)
		mustWrite(t, d.srv, &message.DownstreamOpenResponse{
			RequestID:        downstreamOpenReq.RequestID,
			AssignedStreamID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			ResultCode:       message.ResultCodeSucceeded,
			ResultString:     "OK",
			ExtensionFields:  &message.DownstreamOpenResponseExtensionFields{},
		})
		closeRequest := mustRead(t, d.srv).(*message.DownstreamCloseRequest)
		mustWrite(t, d.srv, &message.DownstreamCloseResponse{
			RequestID:    closeRequest.RequestID,
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "OK",
		})
		assert.Equal(t, &message.Disconnect{
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "NormalClosure",
		}, mustRead(t, d.srv))
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	conn, err := Connect("dummy", TransportTest,
		iscp.WithConnNodeID("11111111-1111-1111-1111-111111111111"),
	)
	require.NoError(t, err)
	defer conn.Close(ctx)

	down, err := conn.OpenDownstream(ctx,
		[]*message.DownstreamFilter{
			{
				SourceNodeID: "22222222-2222-2222-2222-222222222222",
				DataFilters: []*message.DataFilter{
					{Name: "#", Type: "#"},
				},
			},
		},
	)
	require.NoError(t, err)
	require.NoError(t, down.Close(ctx))
}

// TestConn_Close_TimesOutWhenDisconnectSendBlocks は、SendDisconnect の Write が
// 下層 transport でブロックし続けても、Conn.Close が一定時間内に返ることを保証する
// regression テスト。
//
// Task 7 で multi.Transport.Write に再試行を入れたことで、全 sub-connection が
// 未接続の間 multi.Write がブロックし続けるようになった (transport/multi/transport.go)。
// SendDisconnect は ctx を無視して transport.Write を呼ぶため (protocol_session.go の
// SendDisconnect 参照)、全断中の Close がこれに巻き込まれて永久にブロックしていた。
func TestConn_Close_TimesOutWhenDisconnectSendBlocks(t *testing.T) {
	defer goleak.VerifyNone(t)
	d := newDialer(transport.NegotiationParams{})
	RegisterDialer(TransportTest, func() transport.Dialer { return d })

	go func() {
		mockConnectRequest(t, d.srv)
		// 以後は一切 Read しない。Disconnect 送信の Write は下層 pipe で
		// 相手が読むまでブロックし続ける (transport/pipe.go の pipe.Write 参照)。
	}()

	conn, err := Connect("dummy", TransportTest)
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() { done <- conn.Close(context.Background()) }()

	// disconnectSendTimeout（既定 3s）+ 余裕を見て 5s 以内に返ることを期待する。
	select {
	case err := <-done:
		assert.NoError(t, err, "wireConn.Close() の結果がそのまま返るはず")
	case <-time.After(5 * time.Second):
		t.Fatal("Conn.Close did not return within disconnectSendTimeout + margin")
	}
}

func startEchoServer(_ *testing.T) *transport.MessageTransport {
	srv, cli := Pipe()
	go func() {
		for {
			Copy(srv, cli)
		}
	}()
	return cli
}

// TestConnect_ハンドシェイク無応答でタイムアウトする は、サーバーが接続だけ
// 受け付けて ConnectRequest に一切応答しない場合でも、Connect（dial）が
// connectHandshakeTimeout で打ち切られて返ることを検証する。
// transport には read deadline の口がないため、期限超過時に transport を
// close してハンドシェイクのブロックを解除する（newProtocolSession の
// watchdog 参照）。
func TestConnect_ハンドシェイク無応答でタイムアウトする(t *testing.T) {
	defer goleak.VerifyNone(t)
	SetConnectHandshakeTimeout(t, 200*time.Millisecond)

	d := newDialer(transport.NegotiationParams{})
	RegisterDialer(TransportTest, func() transport.Dialer { return d })
	// サーバー側は一切 Read / Write しない。ConnectRequest の Write は下層
	// pipe のランデブーでブロックし続ける。

	done := make(chan error, 1)
	go func() {
		_, err := Connect("dummy", TransportTest)
		done <- err
	}()

	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("Connect did not return while the server never responds to the handshake")
	}
}
