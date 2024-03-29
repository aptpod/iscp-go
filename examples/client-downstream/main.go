// ダウンストリームのサンプル実装パッケージです。
package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os/signal"
	"syscall"
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
		sourceNodeID       string
		nodeSecret         string
		duration           time.Duration
		tokenEndpoint      string
		projectUUID        string
		omitEmptyChunk     bool
		filterType         string
		qos                int // 0:unreliable,1:reliable,2:partial
	)

	flag.StringVar(&tr, "t", "websocket", "Transport")
	flag.StringVar(&address, "a", "localhost:8080", "")
	flag.StringVar(&tokenEndpoint, "te", "http://localhost:8080/api/auth/oauth2/token", "oauth2 token endpoint")
	flag.BoolVar(&enableTLS, "tls", false, "WebSocket EnableTLS")
	flag.BoolVar(&insecureSkipVerify, "k", false, "insecure skip verify **WARNING** This option skips TLSConfig certificate verification.")
	flag.StringVar(&nodeID, "e", "", "nodeID")
	flag.StringVar(&nodeSecret, "s", "", "nodeSecret")
	flag.DurationVar(&connTimeout, "c", time.Second*5, "")
	flag.DurationVar(&duration, "d", time.Second*5, "")
	flag.StringVar(&sourceNodeID, "src", "", "SourceNodeID")
	flag.StringVar(&projectUUID, "p", "00000000-0000-0000-0000-000000000000", "")
	flag.BoolVar(&omitEmptyChunk, "O", false, "omit empty chunk")
	flag.StringVar(&filterType, "tp", "#", "type of filter")
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
				StaticToken: &websocket.Token{Token: tk.AccessToken},
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

	var opts []iscp.DownstreamOption
	if omitEmptyChunk {
		opts = append(opts, iscp.WithDownstreamOmitEmptyChunk())
	}
	opts = append(opts, iscp.WithDownstreamQoS(message.QoS(qos)))

	down, err := conn.OpenDownstream(ctx, []*message.DownstreamFilter{
		{
			SourceNodeID: sourceNodeID,
			DataFilters: []*message.DataFilter{
				{Name: "#", Type: filterType},
			},
		},
	},
		opts...,
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
		fmt.Printf("ReceivedChunk: session_id:%v sequence_number:%v\n", dps.UpstreamInfo.SessionID, dps.SequenceNumber)

		for _, v := range dps.DataPointGroups {
			for _, vv := range v.DataPoints {
				fmt.Printf("DataID: %v ElapsedTime: %v\n", v.DataID.String(), vv.ElapsedTime)
			}
		}
	}
}
