// アップストリームのサンプル実装パッケージです。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/aptpod/iscp-go/errors"
	"github.com/aptpod/iscp-go/iscp"
	"github.com/aptpod/iscp-go/message"
	"github.com/aptpod/iscp-go/transport/websocket"
	"github.com/aptpod/iscp-go/transport/webtransport"
	"github.com/google/uuid"
	"golang.org/x/oauth2/clientcredentials"
)

func main() {
	var (
		tr            string
		address       string
		path          = "/api/iscp/connect"
		connTimeout   time.Duration
		enableTLS     bool
		nodeID        string
		nodeSecret    string
		duration      time.Duration
		tokenEndpoint string
		projectUUID   string
	)

	flag.StringVar(&tr, "t", "websocket", "Transport")
	flag.StringVar(&address, "a", "localhost:8080", "")
	flag.StringVar(&tokenEndpoint, "te", "http://localhost:8080/api/auth/oauth2/token", "oauth2 token endpoint")
	flag.BoolVar(&enableTLS, "tls", false, "WebSocket EnableTLS")
	flag.StringVar(&nodeID, "e", "", "nodeID")
	flag.StringVar(&nodeSecret, "s", "", "nodeSecret")
	flag.DurationVar(&connTimeout, "c", time.Second*5, "")
	flag.DurationVar(&duration, "d", time.Second*5, "")
	flag.StringVar(&projectUUID, "p", "00000000-0000-0000-0000-000000000000", "")
	flag.Parse()

	if nodeID == "" {
		fmt.Printf("required `-e`(node_id) option")
	}
	if nodeSecret == "" {
		fmt.Printf("required `-s`(node_secret) option")
	}
	log.Printf("try to access `%s`", address)

	ctx := context.Background()
	c := clientcredentials.Config{
		ClientID:     nodeID,
		ClientSecret: nodeSecret,
		TokenURL:     tokenEndpoint,
	}
	tkSource := c.TokenSource(ctx)
	tk, err := tkSource.Token()
	if err != nil {
		log.Fatal(err)
	}
	log.Println("succeeded retrieve token")
	conn, err := iscp.Connect(address, iscp.TransportName(tr),
		iscp.WithConnWebSocket(websocket.DialerConfig{
			Path:      path,
			EnableTLS: enableTLS,
			TokenSource: &websocket.StaticTokenSource{
				StaticToken: &websocket.Token{
					Token: tk.AccessToken,
				},
			},
		}),
		iscp.WithConnWebTransport(webtransport.DialerConfig{
			Path: path,
		}),
		iscp.WithConnTokenSource(
			iscp.TokenSourceFunc(func() (iscp.Token, error) {
				tk, err := tkSource.Token()
				if err != nil {
					return "", errors.Errorf("failed to retrieve token by clientcredentials: %w", err)
				}
				return iscp.Token(tk.AccessToken), nil
			}),
		),
		iscp.WithConnNodeID(nodeID),
		iscp.WithConnProjectUUID(uuid.MustParse(projectUUID)),
	)
	if err != nil {
		log.Fatalf("failed to open connection: %v", err)
	}
	defer conn.Close(ctx)

	sessionUUID := uuid.New()
	log.Printf("session uuid: %v", sessionUUID)

	up, err := conn.OpenUpstream(ctx, sessionUUID.String(),
		iscp.WithUpstreamPersist(),
		iscp.WithUpstreamCloseTimeout(time.Minute*10),
		iscp.WithUpstreamReceiveAckHooker(iscp.ReceiveAckHookerFunc(func(streamID uuid.UUID, ack iscp.UpstreamChunkResult) {
			log.Printf("Received Ack. uuid:%v seq:%v result:code %v result:%v", streamID, ack.SequenceNumber, ack.ResultCode, ack.ResultString)
		})),
		iscp.WithUpstreamSendDataPointsHooker(iscp.SendDataPointsHookerFunc(func(streamID uuid.UUID, chunk iscp.UpstreamChunk) {
			log.Printf("Send DataPoints. uuid:%v seq:%v", streamID, chunk.SequenceNumber)
		})),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer up.Close(ctx)

	start := time.Now()

	if err := conn.SendBaseTime(ctx, &message.BaseTime{
		SessionID:   sessionUUID.String(),
		Name:        "edge_rtc",
		Priority:    1000,
		ElapsedTime: time.Since(start),
		BaseTime:    start,
	}, iscp.WithSendMetadataPersist()); err != nil {
		log.Fatal(err)
	}

	var sentCount int
	for {
		if time.Since(start) > duration {
			log.Printf("finished, session uuid:%v totalCount:%v", sessionUUID, sentCount)
			return
		}
		time.Sleep(time.Millisecond * 1000)
		if err := up.WriteDataPoints(context.Background(), &message.DataID{
			Name: "v1/1/string-data",
			Type: "string",
		}, &message.DataPoint{
			ElapsedTime: time.Since(start),
			Payload:     []byte("a"),
		}); err != nil {
			log.Fatal(err)
		}
		sentCount++
		if sentCount%1000 == 0 {
			log.Printf("sent count: %v", sentCount)
		}
	}
}
