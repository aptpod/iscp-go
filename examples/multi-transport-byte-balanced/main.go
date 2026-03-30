// Package main は ByteBalancedSelector を使用したマルチトランスポートのサンプルプログラムです。
//
// このサンプルは、複数の WebSocket 接続を束ねて ByteBalanced（送信バイト数バランス）
// アルゴリズムで送信負荷を均等化する方法を示します。
//
// 実行には iSCP サーバーが必要です。
package main

import (
	"context"
	"fmt"
	stdlog "log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aptpod/iscp-go/v2/log"
	"github.com/aptpod/iscp-go/v2/transport"
	"github.com/aptpod/iscp-go/v2/transport/compress"
	"github.com/aptpod/iscp-go/v2/transport/multi"
	"github.com/aptpod/iscp-go/v2/transport/reconnect"
	"github.com/aptpod/iscp-go/v2/transport/websocket"
)

func main() {
	// サーバーアドレス（環境変数または実際のサーバーアドレスを指定）
	serverAddr1 := getEnvOrDefault("SERVER_ADDR1", "127.0.0.1:8080")
	serverAddr2 := getEnvOrDefault("SERVER_ADDR2", "127.0.0.1:8081")

	logger := log.NewNop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// シグナルハンドリング
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		stdlog.Println("Shutting down...")
		cancel()
	}()

	// 2つの reconnect.Transport を作成
	tr1, err := createReconnectTransport(serverAddr1, "group-1", logger)
	if err != nil {
		stdlog.Fatalf("Failed to create transport1: %v", err)
	}
	defer tr1.Close()

	tr2, err := createReconnectTransport(serverAddr2, "group-1", logger)
	if err != nil {
		stdlog.Fatalf("Failed to create transport2: %v", err)
	}
	defer tr2.Close()

	// 接続が確立されるまで待機
	if err := waitForConnection(ctx, tr1, "transport1"); err != nil {
		stdlog.Fatalf("Transport1 connection failed: %v", err)
	}
	if err := waitForConnection(ctx, tr2, "transport2"); err != nil {
		stdlog.Fatalf("Transport2 connection failed: %v", err)
	}
	stdlog.Println("Both transports connected")

	// ByteBalancedSelector を作成
	transportIDs := []transport.SubConnectionID{"transport1", "transport2"}
	selector := multi.NewByteBalancedSelector(transportIDs)

	// multi.Transport を作成
	mt, err := multi.NewTransport(multi.TransportConfig{
		TransportMap: multi.TransportMap{
			"transport1": tr1,
			"transport2": tr2,
		},
		TransportSelector: selector,
		Logger:            logger,
	})
	if err != nil {
		stdlog.Fatalf("Failed to create multi transport: %v", err)
	}
	defer mt.Close()

	stdlog.Println("Multi transport with ByteBalanced selector created")

	// データ送信ループ
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	counter := 0
	for {
		select {
		case <-ctx.Done():
			// 終了時に統計情報を表示
			stats := selector.Stats()
			stdlog.Printf("Total selections: %d, Switch count: %d", stats.TotalSelections, stats.SwitchCount)
			for id, count := range stats.SelectionCounts {
				stdlog.Printf("  %s: %d selections", id, count)
			}
			return
		case <-ticker.C:
			data := fmt.Appendf(nil, "message-%d", counter)
			if err := mt.Write(data); err != nil {
				stdlog.Printf("Write error: %v", err)
				continue
			}
			counter++
			if counter%100 == 0 {
				stdlog.Printf("Sent %d messages", counter)
			}
		}
	}
}

func createReconnectTransport(addr string, groupID string, logger log.Logger) (*reconnect.Transport, error) {
	return reconnect.Dial(reconnect.DialConfig{
		Dialer: websocket.NewDefaultDialer(),
		DialConfig: transport.DialConfig{
			Address:           addr,
			CompressConfig:    compress.Config{},
			EncodingName:      transport.EncodingNameJSON,
			SuperConnectionID: transport.SuperConnectionID(groupID),
		},
		MaxReconnectAttempts: 10,
		ReconnectInterval:    time.Second,
		Logger:               logger,
	})
}

func waitForConnection(ctx context.Context, tr *reconnect.Transport, name string) error {
	timeout := time.After(10 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout:
			return fmt.Errorf("%s: connection timeout", name)
		case <-ticker.C:
			if tr.Status() == reconnect.StatusConnected {
				return nil
			}
		}
	}
}

func getEnvOrDefault(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}
