// アップストリームのサンプル実装パッケージです。
package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/aptpod/iscp-go/errors"
	"github.com/aptpod/iscp-go/iscp"
	"github.com/aptpod/iscp-go/message"
	"github.com/aptpod/iscp-go/transport/quic"
	"github.com/aptpod/iscp-go/transport/websocket"
	"github.com/aptpod/iscp-go/transport/webtransport"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	var (
		tr                 string
		address            string
		path               = "/api/iscp/connect"
		connTimeout        time.Duration
		enableTLS          bool
		insecureSkipVerify bool
		nodeID             string
		nodeSecret         string
		duration           time.Duration
		tokenEndpoint      string
		projectUUID        string
		qos                int // 0:unreliable,1:reliable,2:partial
	)

	flag.StringVar(&tr, "t", "websocket", "Transport")
	flag.StringVar(&address, "a", "localhost:8080", "")
	flag.StringVar(&tokenEndpoint, "te", "http://localhost:8080/api/auth/oauth2/token", "oauth2 token endpoint")
	flag.BoolVar(&enableTLS, "tls", false, "WebSocket EnableTLS")
	flag.BoolVar(&insecureSkipVerify, "k", false, "insecure skip verify **WARNING** This option skips TLSConfig certificate verification.")
	flag.StringVar(&nodeID, "e", "", "nodeID")
	flag.StringVar(&nodeSecret, "s", "", "nodeSecret")
	flag.DurationVar(&connTimeout, "c", time.Second*5, "connection timeout")
	flag.DurationVar(&duration, "d", time.Second*5, "streaming duration")
	flag.StringVar(&projectUUID, "p", "00000000-0000-0000-0000-000000000000", "project uuid")
	flag.IntVar(&qos, "q", 0, "QoS(0:unreliable,1:reliable,2:partial)")
	flag.Parse()

	if nodeID == "" {
		fmt.Printf("required `-e`(node_id) option")
	}
	if nodeSecret == "" {
		fmt.Printf("required `-s`(node_secret) option")
	}
	log.Printf("try to access `%s`", address)

	ctx := context.Background()
	if insecureSkipVerify {
		ctx = context.WithValue(ctx, oauth2.HTTPClient, &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: insecureSkipVerify,
				},
			},
		})
	}
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
			TLSConfig: &tls.Config{
				InsecureSkipVerify: insecureSkipVerify,
			},
		}),
		iscp.WithConnQUIC(quic.DialerConfig{
			TLSConfig: &tls.Config{
				InsecureSkipVerify: insecureSkipVerify,
			},
		}),
		iscp.WithConnWebTransport(webtransport.DialerConfig{
			Path: path,
			TLSConfig: &tls.Config{
				InsecureSkipVerify: insecureSkipVerify,
			},
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
			fmt.Printf("Received Ack. uuid:%v seq:%v result:code %v result:%v\n", streamID, ack.SequenceNumber, ack.ResultCode, ack.ResultString)
		})),
		iscp.WithUpstreamSendDataPointsHooker(iscp.SendDataPointsHookerFunc(func(streamID uuid.UUID, chunk iscp.UpstreamChunk) {
			fmt.Printf("Send DataPoints. uuid:%v seq:%v\n", streamID, chunk.SequenceNumber)
		})),
		iscp.WithUpstreamQoS(message.QoS(qos)),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer up.Close(ctx)

	start := time.Now()

	if err := conn.SendBaseTime(ctx, &message.BaseTime{
		SessionID:   sessionUUID.String(),
		Name:        "hello-basetime",
		Priority:    20,
		ElapsedTime: time.Since(start),
		BaseTime:    start,
	}, iscp.WithSendMetadataPersist()); err != nil {
		log.Fatal(err)
	}
	if err := conn.SendBaseTime(ctx, &message.BaseTime{
		SessionID:   sessionUUID.String(),
		Name:        "hello-basetime-high-priority",
		Priority:    50,
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
