/*
Package iscpはiSCPプロトコルのクライアント実装パッケージです。

ここではクライアントアクセスまでの一連の流れについて説明します。

# Connect To intdash API

このサンプルではiscp-goを使ってintdash APIに接続します。

	package main

	import (
		"context"
		"flag"
		"fmt"
		"log"

		"github.com/aptpod/iscp-go/errors"
		"github.com/aptpod/iscp-go/iscp"
		"github.com/aptpod/iscp-go/transport/websocket"
		"golang.org/x/oauth2/clientcredentials"
	)

	func main() {
		var (
			tr      string
			address string
			// intdash APIは、トランスポートがWebSocket/WebTransportの場合 `/api/iscp/connect` というパスでiSCP接続を待ち受けます。
			// つまり `http://{host}:{port}/api/iscp/connect` というURLがWebのインターフェースとなります。
			path          = "/api/iscp/connect"
			enableTLS     bool
			nodeID        string
			nodeSecret    string
			tokenEndpoint string
		)

		flag.StringVar(&tr, "t", "websocket", "Transport")
		flag.StringVar(&address, "a", "localhost:8080", "")
		flag.StringVar(&tokenEndpoint, "te", "http://localhost:8080/api/auth/oauth2/token", "oauth2 token endpoint") // OAuth2トークン発行のエンドポイントです。
		flag.BoolVar(&enableTLS, "tls", false, "WebSocket EnableTLS")
		flag.StringVar(&nodeID, "e", "", "nodeID")         // intdash APIでノードを生成した際に発行されたノードUUIDを指定します。
		flag.StringVar(&nodeSecret, "s", "", "nodeSecret") // intdash APIでノードを生成した際に発行されたノードのクライアントシークレットを指定します。
		flag.Parse()

		if nodeID == "" {
			fmt.Printf("required `-e`(node_id) option")
		}
		if nodeSecret == "" {
			fmt.Printf("required `-s`(node_secret) option")
		}
		log.Printf("try to access `%s`", address)

		ctx := context.Background()

		// ノードはOAuth2のクライアントクレデンシャルタイプでトークン交換を行います。
		c := clientcredentials.Config{
			ClientID:     nodeID,
			ClientSecret: nodeSecret,
			TokenURL:     tokenEndpoint,
		}
		tkSource := c.TokenSource(ctx)
		log.Println("succeeded retrieve token")

		conn, err := iscp.Connect(address, iscp.Transport(tr),
			// WebSocketの設定です。 intdash APIへアクセスする場合、`Path` は必ず指定し、`EnableTLS` は必要に応じて変更します。 `EnableTLS` がtrueの場合 `https` アクセスとなります。
			iscp.WithConnWebSocket(websocket.DialerConfig{
				Path:      path,
				EnableTLS: enableTLS,
			}),
			// 接続時に必ずトークンを更新するようにトークンソースを指定します。
			// この実装により接続時に必ず新しいトークンを発行するため、期限切れを考える必要がなくなります。
			iscp.WithConnTokenSource(iscp.TokenSourceFunc(func() (iscp.Token, error) {
				tk, err := tkSource.Token()
				if err != nil {
					return "", errors.Errorf("failed to retrieve token by clientcredentials: %w", err)
				}
				return iscp.Token(tk.AccessToken), nil
			})),
			iscp.WithConnNodeID(nodeID),
		)
		if err != nil {
			log.Fatalf("failed to open connection: %v", err)
		}
		defer conn.Close(ctx)

		log.Println("established connection")

		//  接続後は任意の処理を実装可能です。
	}

# Start Upstream

アップストリームの送信サンプルです。このサンプルでは、基準時刻のメタデータと、文字列型のデータポイントをiSCPサーバーへ送信しています。

	package main

	import (
		"context"
		"log"
		"time"

		"github.com/aptpod/iscp-go/iscp"
		"github.com/aptpod/iscp-go/message"
		"github.com/google/uuid"
	)

	func main() {
		ctx := context.Background()
		conn, err := iscp.Connect("127.0.0.1:8080", iscp.TransportWebSocket,
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
			Priority:    1000,
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

# Start Downstream

前述のアップストリームで送信されたデータをダウンストリームで受信するサンプルです。

このサンプルでは、アップストリーム開始のメタデータ、基準時刻のメタデータ、
文字列型のデータポイントを受信しています。

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
		conn, err := iscp.Connect("127.0.0.1:8080", iscp.TransportWebSocket)
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
*/
package iscp
