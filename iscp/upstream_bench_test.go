package iscp_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	uuid "github.com/google/uuid"

	"github.com/aptpod/iscp-go/v2/iscp"
	. "github.com/aptpod/iscp-go/v2/iscp"
	"github.com/aptpod/iscp-go/v2/message"
	"github.com/aptpod/iscp-go/v2/transport"
)

// BenchmarkUpstream_WriteChunk は、WriteChunk の受理から下層送信・サーバー側の
// Ack 発行までのパイプラインスループットを計測する。
// 送信順序保証（FIFO チケットチェーン）のような送信経路の変更が
// スループットへ与える影響を base/head 比較で確認するために使う。
func BenchmarkUpstream_WriteChunk(b *testing.B) {
	d := newDialer(transport.NegotiationParams{})
	RegisterDialer(TransportTest, func() transport.Dialer { return d })

	var acked atomic.Uint64
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
				_ = srv.WriteMessage(&message.UpstreamChunkAck{
					StreamIDAlias: 1,
					Results: []*message.UpstreamChunkResult{
						{SequenceNumber: m.StreamChunk.SequenceNumber, ResultCode: message.ResultCodeSucceeded, ResultString: "OK"},
					},
					DataIDAliases:   map[uint32]*message.DataID{},
					ExtensionFields: &message.UpstreamChunkAckExtensionFields{},
				})
				acked.Add(1)
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

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	conn, err := Connect("dummy", TransportTest,
		iscp.WithConnNodeID("11111111-1111-1111-1111-111111111111"),
	)
	if err != nil {
		b.Fatal(err)
	}
	up, err := conn.OpenUpstream(ctx, "session_id",
		WithUpstreamAckInterval(time.Millisecond),
		WithUpstreamQoS(message.QoSReliable),
		WithUpstreamFlushPolicyImmediately(),
	)
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := up.WriteChunk(ctx, &DataPointGroup{
			DataID:     &message.DataID{Name: "name", Type: "type"},
			DataPoints: DataPoints{{ElapsedTime: time.Second, Payload: []byte{1}}},
		}); err != nil {
			b.Fatal(err)
		}
	}
	// 受理した全 chunk がサーバーへ到着するまでを計測に含める（送信は非同期のため、
	// ここで待たないと WriteChunk の受理コストしか測れない）。
	deadline := time.Now().Add(60 * time.Second)
	for acked.Load() < uint64(b.N) {
		if time.Now().After(deadline) {
			b.Fatalf("acked=%d < N=%d", acked.Load(), b.N)
		}
		time.Sleep(50 * time.Microsecond)
	}
	b.StopTimer()

	if err := up.Close(ctx); err != nil {
		b.Logf("upstream close: %v", err)
	}
	if err := conn.Close(ctx); err != nil {
		b.Logf("conn close: %v", err)
	}
	<-srvDone
}
