package iscp

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/aptpod/iscp-go/encoding"
	"github.com/aptpod/iscp-go/encoding/protobuf"
	"github.com/aptpod/iscp-go/log"
	"github.com/aptpod/iscp-go/message"
	"github.com/aptpod/iscp-go/transport"
	"github.com/aptpod/iscp-go/wire"
)

// newTestClientConnPairは、Downstreamのwireテスト用に、ハンドシェイクを完了させた
// wire.ClientConnと、その相手側（サーバー相当）のトランスポートを返します。
//
// サーバー側は接続確立以降、明示的にRead/Writeするまで何もしないため、
// テストからAckの書き込みをブロックさせるなど、送受信タイミングを制御できます。
func newTestClientConnPair(t *testing.T) (cliConn *wire.ClientConn, srv wire.EncodingTransport) {
	t.Helper()
	cliTr, srvTr := transport.Pipe()
	srv = encoding.NewTransport(&encoding.TransportConfig{
		Transport: srvTr,
		Encoding:  protobuf.NewEncoding(),
	})
	cli := encoding.NewTransport(&encoding.TransportConfig{
		Transport: cliTr,
		Encoding:  protobuf.NewEncoding(),
	})

	handshakeDone := make(chan struct{})
	go func() {
		defer close(handshakeDone)
		if _, err := srv.Read(); err != nil { // ConnectRequest
			return
		}
		_ = srv.Write(&message.ConnectResponse{
			ProtocolVersion: "3.0.0",
			ResultCode:      message.ResultCodeSucceeded,
			ExtensionFields: &message.ConnectResponseExtensionFields{},
		})
	}()

	conn, err := wire.Connect(&wire.ClientConnConfig{
		Transport: cli,
		Logger:    log.NewNop(),
	})
	require.NoError(t, err)
	<-handshakeDone
	t.Cleanup(func() {
		_ = conn.Close()
	})
	return conn, srv
}

// mustReadIgnoringPingPongは、Ping/Pongを読み飛ばしながらメッセージを読み込みます。
// keepAliveLoopは接続確立直後からPingを送るため、Pongを返送してタイムアウトによる
// 切断を防ぎます。
func mustReadIgnoringPingPong(t *testing.T, tr wire.EncodingTransport) message.Message {
	t.Helper()
	for {
		msg, err := tr.Read()
		require.NoError(t, err)
		switch m := msg.(type) {
		case *message.Ping:
			require.NoError(t, tr.Write(&message.Pong{
				RequestID:       m.RequestID,
				ExtensionFields: &message.PongExtensionFields{},
			}))
			continue
		case *message.Pong:
			continue
		}
		return msg
	}
}

// TestDownstreamFlushAckReleasesMutexBeforeWriteは、flushAckがAckの書き込み中
// d.muを保持しないことを検証します。
//
// RED理由: 修正前はflushAckがd.mu.Lock()を保持したままAckの書き込みを行うため、
// 書き込みがブロックしている間、他のgoroutineからのpushResultAckBufferも
// 巻き添えでブロックします。
func TestDownstreamFlushAckReleasesMutexBeforeWrite(t *testing.T) {
	wireConn, srv := newTestClientConnPair(t)

	d := &Downstream{
		ctx:                   context.Background(),
		wireConn:              wireConn,
		idAlias:               1,
		chunkAckIDSequence:    newSequenceNumberGenerator(0),
		upstreamInfoAckBuffer: make(map[uint32]*message.UpstreamInfo),
		dataIDAckBuffer:       make(map[uint32]*message.DataID),
		resultAckBuffer:       make([]*message.DownstreamChunkResult, 0, 1),
	}

	first := &message.DownstreamChunkResult{ResultCode: message.ResultCodeSucceeded, ResultString: "first"}
	second := &message.DownstreamChunkResult{ResultCode: message.ResultCodeSucceeded, ResultString: "second"}
	d.pushResultAckBuffer(first)

	flushDone := make(chan error, 1)
	go func() {
		flushDone <- d.flushAck()
	}()

	// flushAckがAckの書き込みに入り、サーバー側がまだ読んでいないため
	// 書き込みがブロックしている状態になる猶予を与える。
	time.Sleep(50 * time.Millisecond)

	pushDone := make(chan struct{})
	go func() {
		d.pushResultAckBuffer(second)
		close(pushDone)
	}()

	select {
	case <-pushDone:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("pushResultAckBuffer blocked while ack was being written")
	}

	ackMsg := mustReadIgnoringPingPong(t, srv)
	ack, ok := ackMsg.(*message.DownstreamChunkAck)
	require.True(t, ok, "unexpected message type %T", ackMsg)
	require.Len(t, ack.Results, 1)
	require.Equal(t, first.ResultString, ack.Results[0].ResultString)

	require.NoError(t, <-flushDone)
}
