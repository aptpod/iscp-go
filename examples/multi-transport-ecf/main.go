// Package main は ECFSelector を使用したマルチトランスポートのサンプルプログラムです。
//
// このサンプルは、複数の WebSocket 接続を束ねて ECF (Earliest Completion First)
// アルゴリズムで最適なトランスポートを選択する方法を示します。
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

	"github.com/aptpod/iscp-go/log"
	"github.com/aptpod/iscp-go/transport"
	"github.com/aptpod/iscp-go/transport/compress"
	"github.com/aptpod/iscp-go/transport/multi"
	"github.com/aptpod/iscp-go/transport/reconnect"
	"github.com/aptpod/iscp-go/transport/websocket"

	// WebSocket エンコーダを登録
	_ "github.com/aptpod/iscp-go/transport/websocket/coder"
)

func main() {
	// サーバーアドレス（環境変数または実際のサーバーアドレスを指定）
	serverAddr1 := getEnvOrDefault("SERVER_ADDR1", "127.0.0.1:8080")
	serverAddr2 := getEnvOrDefault("SERVER_ADDR2", "127.0.0.1:8081")

	// Nop ロガーを使用（実環境ではカスタムロガーを使用）
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
	tr1, err := createReconnectTransport(serverAddr1, "transport-1", logger)
	if err != nil {
		stdlog.Fatalf("Failed to create transport1: %v", err)
	}
	defer tr1.Close()

	tr2, err := createReconnectTransport(serverAddr2, "transport-2", logger)
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

	// ECFSelector を作成
	selector := multi.NewECFSelector()

	// multi.Transport を作成
	transportMap := multi.TransportMap{
		"transport1": tr1,
		"transport2": tr2,
	}

	mt, err := multi.NewTransport(multi.TransportConfig{
		TransportMap:      transportMap,
		TransportSelector: selector,
		Logger:            logger,
	})
	if err != nil {
		stdlog.Fatalf("Failed to create multi transport: %v", err)
	}
	defer mt.Close()

	stdlog.Println("Multi transport with ECF selector created")

	// メトリクスの出力開始
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				printMetrics(tr1, "transport1")
				printMetrics(tr2, "transport2")
			}
		}
	}()

	// データ送信のシミュレーション
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	counter := 0
	for {
		select {
		case <-ctx.Done():
			stdlog.Println("Context canceled, exiting")
			return
		case <-ticker.C:
			data := []byte(fmt.Sprintf("message-%d", counter))
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
			Address:          addr,
			CompressConfig:   compress.Config{},
			EncodingName:     transport.EncodingNameJSON,
			TransportGroupID: transport.TransportGroupID(groupID),
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

func printMetrics(tr *reconnect.Transport, name string) {
	stdlog.Printf("[%s] RTT: %v, RTTVar: %v, CWND: %d, BytesInFlight: %d",
		name,
		tr.RTT(),
		tr.RTTVar(),
		tr.CongestionWindow(),
		tr.BytesInFlight(),
	)
}

func getEnvOrDefault(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}
