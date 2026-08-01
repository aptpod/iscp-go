package reconnect_test

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/aptpod/iscp-go/v2/log"
	"github.com/aptpod/iscp-go/v2/transport"
	. "github.com/aptpod/iscp-go/v2/transport/reconnect"
)

// gatedCloseTransport は mockCountingTransport の Close をゲートするラッパー。
// 最初の Close は entered を通知して release までブロックする。doReconnect の
// old.Close() を意図的に長引かせ、その間に旧トランスポートへ読み取り結果を
// 積むために使う。
type gatedCloseTransport struct {
	*mockCountingTransport
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (g *gatedCloseTransport) Close() error {
	g.once.Do(func() {
		close(g.entered)
		<-g.release
	})
	return g.mockCountingTransport.Close()
}

func (g *gatedCloseTransport) CloseWithStatus(_ transport.CloseStatus) error {
	return g.Close()
}

func iscpMsg(payload ...byte) []byte {
	return append([]byte{byte(MessageTypeISCP)}, payload...)
}

// TestReconnectTransport_readLoopが旧世代の未消化読み取りで停止しない は、
// readLoop がリーダー goroutine の終了を待つ間、readCh に残った未消化の
// 読み取り結果が排出されることを検証する。
//
// 発火条件: processReads がハートビートタイムアウト（timerC）経路で
// reconnect に入った後は、誰も readCh を受信しない。その間にリーダーが
// 結果を 2 件生産すると、1 件目が readCh（cap 1）を満たし、2 件目の送信で
// リーダーがブロックする。この送信は tr.Close() では解除されず（Read 中では
// ないため）、r.ctx も生きているため、reconnect が成功しても readLoop が
// リーダーの終了待ちで永久に停止する（新トランスポートのデータが二度と
// 流れない）。ネットワーク停滞でタイムアウトが発火した直後に滞留データが
// バーストで届く、という現実的なシーケンスで踏む。
//
// テストは doReconnect の old.Close() をゲートで止め、その間に旧トランス
// ポートへ 2 件のデータを届けて「バッファ 1 件 + 送信ブロック 1 件」を
// 決定的に作る。オラクルは再接続後の新トランスポートのデータが公開 Read
// から取得できること。
func TestReconnectTransport_readLoopが旧世代の未消化読み取りで停止しない(t *testing.T) {
	defer goleak.VerifyNone(t)

	mock1 := &gatedCloseTransport{
		mockCountingTransport: newMockCountingTransport("mock1"),
		entered:               make(chan struct{}),
		release:               make(chan struct{}),
	}
	mock2 := newMockCountingTransport("mock2")

	var mu sync.Mutex
	dialCount := 0
	tr, err := Dial(DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			mu.Lock()
			defer mu.Unlock()
			dialCount++
			if dialCount == 1 {
				return mock1, nil
			}
			return mock2, nil
		}),
		DialConfig: transport.DialConfig{
			SubConnectionID: "sub1",
			TransportType:   transport.NegotiationNameWebSocket, // v4（ハートビートタイムアウトあり）
		},
		MaxReconnectAttempts: -1,
		ReconnectInterval:    10 * time.Millisecond,
		HeartbeatInterval:    time.Hour,
		HeartbeatTimeout:     300 * time.Millisecond,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)

	waitForTransportConnected(t, tr)

	// データを流さず、ハートビートタイムアウト → reconnect → old.Close() の
	// ゲート到達を待つ。
	select {
	case <-mock1.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("heartbeat timeout did not trigger reconnect")
	}

	// old.Close() がブロックしている間に旧トランスポートへ 2 件届ける。
	// リーダーが両方消化する（1 件目が readCh を満たし、2 件目の送信で
	// ブロックする）のを待ってからゲートを解放する。
	mock1.readCh <- iscpMsg(1)
	mock1.readCh <- iscpMsg(2)
	require.Eventually(t,
		func() bool { return len(mock1.readCh) == 0 },
		5*time.Second, time.Millisecond,
		"reader should consume both pending reads",
	)
	close(mock1.release)

	// 再接続完了後、新トランスポートのデータが公開 Read から取得できること。
	// readLoop が旧リーダーの終了待ちで停止していると、ここが進まない。
	waitForTransportConnected(t, tr)
	mock2.readCh <- iscpMsg(3)

	readDone := make(chan []byte, 1)
	go func() {
		if bs, err := tr.Read(); err == nil {
			readDone <- bs
		}
	}()
	select {
	case bs := <-readDone:
		require.Equal(t, []byte{3}, bs)
	case <-time.After(3 * time.Second):
		t.Fatal("data from the new transport was not delivered: readLoop is stuck waiting for the old reader")
	}

	require.NoError(t, tr.Close())
}
