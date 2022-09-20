// ダウンストリームのサンプル実装パッケージです。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os/signal"
	"syscall"
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
		sourceNodeID  string
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
	flag.StringVar(&sourceNodeID, "src", "", "SourceNodeID")
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

	conn, err := iscp.Connect(address, iscp.Transport(tr),
		iscp.WithConnWebSocket(websocket.DialerConfig{
			Path:      path,
			EnableTLS: enableTLS,
			TokenSource: &websocket.StaticTokenSource{
				StaticToken: &websocket.Token{Token: tk.AccessToken},
			},
		}),
		iscp.WithConnWebTransport(webtransport.DialerConfig{
			Path: path,
		}),
		iscp.WithConnTokenSource(iscp.TokenSourceFunc(func() (iscp.Token, error) {
			tk, err := tkSource.Token()
			if err != nil {
				return "", errors.Errorf("failed to retrieve token by clientcredentials: %w", err)
			}
			return iscp.Token(tk.AccessToken), nil
		})),
		iscp.WithConnNodeID(nodeID),
		iscp.WithConnProjectUUID(uuid.MustParse(projectUUID)),
	)
	if err != nil {
		log.Fatalf("failed to open connection: %v", err)
	}
	defer conn.Close(ctx)

	down, err := conn.OpenDownstream(ctx, []*message.DownstreamFilter{
		{
			SourceNodeID: sourceNodeID,
			DataFilters: []*message.DataFilter{
				{Name: "#", Type: "#"},
			},
		},
	},
	)
	if err != nil {
		log.Fatal(err)
	}
	defer down.Close(ctx)

	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGTERM)
	defer cancel()

	for {
		dps, err := down.ReadDataPoints(ctx)
		if err != nil {
			log.Println(err)
			return
		}
		fmt.Printf("DEBUG: upstream id %v seq_num: %v\n", dps.UpstreamInfo.StreamID.ID(), dps.SequenceNumber)
		for _, v := range dps.DataPointGroups {
			fmt.Printf("DEBUG: v %v \n", v)
		}
	}
}
