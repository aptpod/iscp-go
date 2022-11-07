package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"
	"time"

	"github.com/aptpod/iscp-go/iscp"
	"github.com/aptpod/iscp-go/message"
)

func main() {
	ctx := context.Background()
	conn, err := iscp.Connect("127.0.0.1:8080", iscp.TransportNameWebSocket)
	if err != nil {
		log.Fatalf("failed to open connection: %v", err)
	}
	defer conn.Close(ctx)

	down, err := conn.OpenDownstream(ctx, []*message.DownstreamFilter{
		{
			SourceNodeID: "40112819-9352-4742-8244-d47885f882ed", // 送信元のノードIDを指定します。
			DataFilters: []*message.DataFilter{
				{Name: "#", Type: "#"}, // 受信したいデータを名称と型で指定します。この例では、ワイルドカード `#` を使用して全てのデータを取得します。
			},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer down.Close(ctx)

	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGTERM)
	defer cancel()

	meta, err := down.ReadMetadata(ctx)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Received a metadata source_node_id[%v], metadata_type:[%T]", meta.SourceNodeID, meta.Metadata)
	if upstreamOpen, ok := meta.Metadata.(*message.UpstreamOpen); ok { // 型アサーションを利用してメタデータの種類を識別します。
		log.Printf("Received upstream_open session_id[%s]", upstreamOpen.SessionID)
	}

	meta, err = down.ReadMetadata(ctx)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Received a metadata source_node_id[%v], metadata_type:[%T]", meta.SourceNodeID, meta.Metadata)
	if baseTime, ok := meta.Metadata.(*message.BaseTime); ok { // 型アサーションを利用してメタデータの種類を識別します。
		log.Printf("Received base_time[%s], priority[%v], name[%s]", baseTime.BaseTime.Format(time.RFC3339Nano), baseTime.Priority, baseTime.Name)
	}

	dps, err := down.ReadDataPoints(ctx)
	if err != nil {
		log.Fatal(err)
		return
	}
	log.Printf("Received data_points sequence_number[%d], session_id[%s]", dps.SequenceNumber, dps.UpstreamInfo.SessionID)
	for _, dpg := range dps.DataPointGroups {
		for _, dp := range dpg.DataPoints {
			log.Printf("Received a data_point data_name[%s], data_type[%s] payload[%s]", dpg.DataID.Name, dpg.DataID.Type, string(dp.Payload))
		}
	}
}
