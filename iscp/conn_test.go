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

	"github.com/aptpod/iscp-go/iscp"
	. "github.com/aptpod/iscp-go/iscp"
	"github.com/aptpod/iscp-go/log"
	"github.com/aptpod/iscp-go/message"
	"github.com/aptpod/iscp-go/transport"
	"github.com/aptpod/iscp-go/transport/compress"
	"github.com/aptpod/iscp-go/wire"
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

// blockingDialer は、Dial() 呼び出し時に release がcloseされるまでブロックするテスト用ダイヤラー。
// started は Dial() に入ったタイミングで通知され、再接続のdial待ち中にCloseを発生させる
// タイミングを外部から制御するために使用する。
type blockingDialer struct {
	*dialer
	started chan struct{}
	release chan struct{}
}

func (b *blockingDialer) Dial(c transport.DialConfig) (transport.Transport, error) {
	close(b.started)
	<-b.release
	return b.dialer.Dial(c)
}

// Test_Conn_Reconnect_CloseDuringDialDoesNotBlock は、再接続のdial待ち中に
// Close(ctx)が呼ばれた場合、dialの完了を待たずに（wireConnMuの保持区間が
// 短時間化されたことで）即座に返ることを検証する。
//
// 修正前は reconnect() が dial のリトライ全体で wireConnMu を保持していたため、
// Close() は dial が完了する（またはstate.Is(Closed)を検知して次のリトライ
// 判定タイミングで諦める）までブロックされていた。
func Test_Conn_Reconnect_CloseDuringDialDoesNotBlock(t *testing.T) {
	defer goleak.VerifyNone(t)
	ctx := context.Background()

	ds0 := newDialer(transport.NegotiationParams{})
	ds1 := newDialer(transport.NegotiationParams{})

	started := make(chan struct{})
	release := make(chan struct{})
	bd := &blockingDialer{dialer: ds1, started: started, release: release}

	var callCount int
	RegisterDialer(TransportTest,
		func() transport.Dialer {
			callCount++
			if callCount == 1 {
				return ds0
			}
			return bd
		},
	)

	done0 := make(chan struct{})
	go func() {
		defer close(done0)
		mockConnectRequest(t, ds0.srv)
	}()

	// 2回目のdialが完了した後、ハンドシェイクに応答する。Close()は
	// このdialの完了を待たずに返るはずなので、reconnect()はハンドシェイク後に
	// stateがClosedであることを検知し、確立したセッションを閉じる。
	//
	// このとき reconnect() が辿る経路は、Close() との競合具合によって
	// 2 通りありうる（いずれも正常; conn.go の reconnect() にある
	// 「ここで res.Close を呼ぶと二重 close になる」というコメント参照）。
	//   - 経路A（通常）: state が Closed であることを検知し、reconnect()
	//     が確立したセッションを自ら閉じる（Disconnect は送信されない）。
	//   - 経路B（低確率）: state の検知と CompareAndSwap の間に Close()
	//     が state を変更する。この場合 reconnect() は wireConn への代入
	//     のみ行い自らは close せず、close() 側が Disconnect を送信して
	//     から close する。
	// どちらの経路でも確立した wireConn は最終的に 1 回だけ close される
	// ため、Disconnect が読めた場合は読み飛ばし、最終的に読み取りが
	// エラー（EOF 等）になることを確認する。
	// このゴルーチン内の検証には assert を使うこと: require / t.Fatal は
	// FailNow (runtime.Goexit) を呼ぶが、テスト本体のゴルーチン以外から
	// 呼ぶと後続の <-done1 待ちが解放されずテスト全体がハングする。
	done1 := make(chan struct{})
	go func() {
		defer close(done1)
		mockConnectRequest(t, ds1.srv)

		deadline := time.After(5 * time.Second)
		for {
			readDone := make(chan struct{})
			var msg message.Message
			var err error
			go func() {
				defer close(readDone)
				msg, err = ds1.srv.Read()
			}()
			select {
			case <-readDone:
			case <-deadline:
				assert.Fail(t, "ds1.srv was not closed within the deadline")
				return
			}
			if err != nil {
				// wireConn が close された（EOF 等）。期待どおりの終了。
				return
			}
			if ping, ok := msg.(*message.Ping); ok {
				// keepalive の Ping は経路に関わらず届きうる。応答しないと
				// 相手のハンドシェイク/生存監視待ちを妨げるため Pong を返す。
				if err := ds1.srv.Write(&message.Pong{
					RequestID:       ping.RequestID,
					ExtensionFields: &message.PongExtensionFields{},
				}); err != nil {
					return
				}
				continue
			}
			if _, ok := msg.(*message.Disconnect); ok {
				// 経路B（コメント参照）: close() が送る Disconnect。読み飛ばして
				// 次の読み取り（最終的な close によるエラー）を待つ。
				continue
			}
			assert.Failf(t, "unexpected message before close", "%T", msg)
			return
		}
	}()

	conn, err := Connect("dummy", TransportTest, WithConnLogger(log.NewStd()))
	require.NoError(t, err)
	<-done0

	// 旧トランスポートを閉じ、バックグラウンドの再接続をトリガーする。
	ds0.srv.Close()

	// reconnect() がdial中（wireConnMuを保持せずリトライ中）であることを確認する。
	<-started

	// dialが完了する前にClose()を呼ぶ。(a)の修正により reconnect() は dial 中
	// wireConnMu を保持しないため、Close() は dial の完了を待たずに返るはず。
	closeDone := make(chan error, 1)
	go func() { closeDone <- conn.Close(ctx) }()

	select {
	case err := <-closeDone:
		assert.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Close() did not return promptly: still blocked on the in-flight dial")
	}

	// Close()完了後にdialを完了させる。reconnect()はstateが既にClosedである
	// ことを検知し、panicせずにErrConnectionClosedを返し、確立したwireConnを
	// 自身でcloseする必要がある（漏れると goleak がリークを検出する）。
	close(release)

	<-done1
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
		DialerMap: map[transport.TransportID]transport.Dialer{
			"transport-1": d1,
		},
	}))
	if !assert.NoError(t, err) {
		close(done)
		return
	}
	defer conn.Close(ctx)
}

func startEchoServer(_ *testing.T) wire.EncodingTransport {
	srv, cli := Pipe()
	go func() {
		for {
			Copy(srv, cli)
		}
	}()
	return cli
}
