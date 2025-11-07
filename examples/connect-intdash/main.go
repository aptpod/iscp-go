package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log"

	"golang.org/x/oauth2/clientcredentials"

	"github.com/aptpod/iscp-go/errors"
	"github.com/aptpod/iscp-go/iscp"
	"github.com/aptpod/iscp-go/transport/websocket"
)

func main() {
	var (
		tr      string
		address string
		// intdash APIは、トランスポートがWebSocket/WebTransportの場合 `/api/iscp/connect` というパスでiSCP接続を待ち受けます。
		// つまり `http://{host}:{port}/api/iscp/connect` というURLがWebのインターフェースとなります。
		path               = "/api/iscp/connect"
		enableTLS          bool
		insecureSkipVerify bool
		nodeID             string
		nodeSecret         string
		tokenEndpoint      string
	)

	flag.StringVar(&tr, "t", "websocket", "Transport")
	flag.StringVar(&address, "a", "localhost:8080", "")
	flag.StringVar(&tokenEndpoint, "te", "http://localhost:8080/api/auth/oauth2/token", "oauth2 token endpoint") // OAuth2トークン発行のエンドポイントです。
	flag.BoolVar(&enableTLS, "tls", false, "WebSocket EnableTLS")
	flag.BoolVar(&insecureSkipVerify, "k", false, "insecure skip verify **WARNING** This option skips TLSConfig certificate verification.")
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

	conn, err := iscp.Connect(address, iscp.TransportName(tr),
		// WebSocketの設定です。 intdash APIへアクセスする場合、`Path` は必ず指定し、`EnableTLS` は必要に応じて変更します。 `EnableTLS` がtrueの場合 `https` アクセスとなります。
		iscp.WithConnWebSocket(websocket.DialerConfig{
			Path:      path,
			EnableTLS: enableTLS,
			TLSConfig: &tls.Config{
				InsecureSkipVerify: insecureSkipVerify,
			},
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
