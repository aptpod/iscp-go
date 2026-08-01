package reconnect_test

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/aptpod/iscp-go/v2/log"
	"github.com/aptpod/iscp-go/v2/transport"
	. "github.com/aptpod/iscp-go/v2/transport/reconnect"
)

var errDialRefused = errors.New("test: dial refused")

// failAfterFirstDialer は、初回の dial だけ mock を返し、以降は即エラーを
// 返す dialer。「再接続の試行が失敗し続ける」状況を模擬する。
type failAfterFirstDialer struct {
	mock      transport.Transport
	dialCount atomic.Int32
}

func (d *failAfterFirstDialer) Dial(transport.DialConfig) (transport.Transport, error) {
	if d.dialCount.Add(1) == 1 {
		return d.mock, nil
	}
	return nil, errDialRefused
}

// alwaysFailDialer は、常に即エラーを返す dialer。「初期接続が失敗し続ける」
// 状況を模擬する。
type alwaysFailDialer struct {
	dialCount atomic.Int32
}

func (d *alwaysFailDialer) Dial(transport.DialConfig) (transport.Transport, error) {
	d.dialCount.Add(1)
	return nil, errDialRefused
}

// TestReconnectTransport_再接続の試行間待機がCloseで中断される は、doReconnect
// の試行間待機（reconnectInterval）が r.ctx のキャンセルで打ち切られることを
// 検証する。
//
// doReconnect は reconnectMu を保持したまま試行間待機するため、この待機が
// 中断できないと、CloseWithStatus（cancel → waitForReconnectToFinish で
// reconnectMu を待つ）が最大 reconnectInterval だけ余分にブロックする。
// reconnectInterval は外部設定値で上限がなく、Close の所要時間が設定値に
// 比例して伸びる。
//
// 修正前: Close が待機満了（5 秒）まで返らず、3 秒の検出でタイムアウトする。
func TestReconnectTransport_再接続の試行間待機がCloseで中断される(t *testing.T) {
	defer goleak.VerifyNone(t)

	mock := newMockCountingTransport("mock1")
	d := &failAfterFirstDialer{mock: mock}

	tr, err := Dial(DialConfig{
		Dialer: d,
		DialConfig: transport.DialConfig{
			SubConnectionID: "sub1",
			TransportType:   transport.NegotiationNameWebSocket,
		},
		MaxReconnectAttempts: -1,
		ReconnectInterval:    5 * time.Second,
		HeartbeatInterval:    time.Hour,
		HeartbeatTimeout:     time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)

	waitForTransportConnected(t, tr)

	// 下層を落として再接続をトリガーし、2 回目の dial が失敗して試行間
	// 待機に入るのを待つ。
	mock.Close()
	require.Eventually(t,
		func() bool { return d.dialCount.Load() >= 2 },
		5*time.Second, 10*time.Millisecond,
		"reconnect should attempt a second dial and fail into the interval wait",
	)
	require.Eventually(t,
		func() bool { return tr.Status() == StatusReconnecting },
		5*time.Second, 10*time.Millisecond,
	)

	closeDone := make(chan error, 1)
	start := time.Now()
	go func() { closeDone <- tr.Close() }()

	select {
	case err := <-closeDone:
		require.NoError(t, err)
		assert.Less(t, time.Since(start), 2*time.Second,
			"Close should not wait out the full reconnectInterval")
	case <-time.After(3 * time.Second):
		assert.Fail(t, "Close() did not return within 3s: the inter-attempt wait is not interruptible")
		<-closeDone // 後始末（待機満了後には返ってくる）
	}
}

// TestReconnectTransport_初期接続の試行間待機がCloseで中断される は、
// initialConnect の試行間待機（reconnectInterval）が r.ctx のキャンセルで
// 打ち切られることを検証する。
//
// CloseWithStatus は initialConnect の完了を待たない（reconnectMu を取ら
// ない）ため Close 自体は即返るが、待機が中断できないと initialConnect
// goroutine が Close 後も最大 reconnectInterval 残留する。goleak が残留を
// 検出する。
func TestReconnectTransport_初期接続の試行間待機がCloseで中断される(t *testing.T) {
	defer goleak.VerifyNone(t)

	d := &alwaysFailDialer{}

	tr, err := Dial(DialConfig{
		Dialer: d,
		DialConfig: transport.DialConfig{
			SubConnectionID: "sub1",
			TransportType:   transport.NegotiationNameWebSocket,
		},
		MaxReconnectAttempts: -1,
		ReconnectInterval:    5 * time.Second,
		HeartbeatInterval:    time.Hour,
		HeartbeatTimeout:     time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)

	// 初回の dial が失敗して試行間待機に入るのを待ってから Close する。
	require.Eventually(t,
		func() bool { return d.dialCount.Load() >= 1 },
		5*time.Second, 10*time.Millisecond,
		"initial connect should attempt a dial and fail into the interval wait",
	)
	require.NoError(t, tr.Close())
}
