package reconnect_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aptpod/iscp-go/v2/log"
	"github.com/aptpod/iscp-go/v2/transport"
	. "github.com/aptpod/iscp-go/v2/transport/reconnect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// ctxAwareBlockingDialer は、初回の dial は mock を返し、2 回目以降は
// ctx.Done() までブロックする transport.ContextDialer 実装。
// 「dial がハングするが ctx は尊重する」dialer を模擬する。
type ctxAwareBlockingDialer struct {
	mock      transport.Transport
	dialCount atomic.Int32
}

func (d *ctxAwareBlockingDialer) Dial(dc transport.DialConfig) (transport.Transport, error) {
	return d.DialContext(context.Background(), dc)
}

func (d *ctxAwareBlockingDialer) DialContext(ctx context.Context, _ transport.DialConfig) (transport.Transport, error) {
	if d.dialCount.Add(1) == 1 {
		return d.mock, nil
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

// TestReconnectTransport_Reconnect中のClose_ContextDialerなら中断されて返る は、
// 再接続の dial がブロックしていても、dialer が ContextDialer を実装していれば
// CloseWithStatus の cancel() が dial を中断し、Close() が有限時間で返ることを
// 検証する。
//
// TestReconnectTransport_Reconnect中にClose（concurrent_close_test.go）が記録
// している P6（従来型 Dialer では Close が dial 解放まで戻れない）の解消版。
// 従来型 Dialer のフォールバック挙動は引き続き P6 テストが記録している。
// goleak により、中断された dial goroutine が残留しないことも同時に検証する。
func TestReconnectTransport_Reconnect中のClose_ContextDialerなら中断されて返る(t *testing.T) {
	defer goleak.VerifyNone(t)

	mock := newMockCountingTransport("mock1")
	d := &ctxAwareBlockingDialer{mock: mock}

	tr, err := Dial(DialConfig{
		Dialer: d,
		DialConfig: transport.DialConfig{
			SubConnectionID: "sub1",
			TransportType:   transport.NegotiationNameWebSocket,
		},
		MaxReconnectAttempts: -1,
		ReconnectInterval:    10 * time.Millisecond,
		HeartbeatInterval:    time.Hour,
		HeartbeatTimeout:     time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)

	waitForTransportConnected(t, tr)

	// 下層を落として再接続をトリガーし、2 回目の dial（ctx 待ちブロック）に
	// 入るまで待つ。
	mock.Close()
	require.Eventually(t,
		func() bool { return d.dialCount.Load() >= 2 },
		5*time.Second, 10*time.Millisecond,
		"dialer should be invoked a second time and block on ctx",
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
		assert.Less(t, time.Since(start), time.Second,
			"Close should return promptly by canceling the in-flight dial")
	case <-time.After(3 * time.Second):
		assert.Fail(t, "Close() did not return within 3s while dial is blocked (dial ctx not honored)")
	}
}
