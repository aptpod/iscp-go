package main

import (
	"context"
	"log"
	"time"

	"github.com/google/uuid"

	"github.com/aptpod/iscp-go/iscp"
	"github.com/aptpod/iscp-go/message"
)

func main() {
	ctx := context.Background()
	conn, err := iscp.Connect("127.0.0.1:8080", iscp.TransportNameWebSocket,
		iscp.WithConnNodeID("40112819-9352-4742-8244-d47885f882ed"), // 任意のノードIDを指定します。ここで指定したノードIDが送信元のノードとなります。
	)
	if err != nil {
		log.Fatalf("failed to open connection: %v", err)
	}
	defer conn.Close(ctx)

	sessionUUID := uuid.New() // セッションIDを払い出します。
	log.Printf("session uuid: %v", sessionUUID)

	up, err := conn.OpenUpstream(ctx, sessionUUID.String())
	if err != nil {
		log.Fatal(err)
	}
	defer up.Close(ctx)

	baseTime := time.Now() // 基準時刻です。

	// 基準時刻をiSCPサーバーへ送信します。
	if err := conn.SendBaseTime(ctx, &message.BaseTime{
		SessionID:   sessionUUID.String(),
		Name:        "manual",
		Priority:    100,
		ElapsedTime: time.Since(baseTime),
		BaseTime:    baseTime,
	}, iscp.WithSendMetadataPersist()); err != nil {
		log.Fatal(err)
	}

	time.Sleep(time.Second)
	// データポイントをiSCPサーバーへ送信します。
	if err := up.WriteDataPoints(ctx, &message.DataID{
		Name: "greeting",
		Type: "string",
	}, &message.DataPoint{
		ElapsedTime: time.Since(baseTime), // 基準時刻からの経過時間をデータポイントの経過時間として打刻します。
		Payload:     []byte("hello"),
	}); err != nil {
		log.Fatal(err)
	}
}
