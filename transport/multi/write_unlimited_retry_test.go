package multi_test

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aptpod/iscp-go/v2/log"
	"github.com/aptpod/iscp-go/v2/transport"
	. "github.com/aptpod/iscp-go/v2/transport/multi"
	"github.com/aptpod/iscp-go/v2/transport/reconnect"
	"github.com/stretchr/testify/require"
)

// TestReconnectTransport_Write_DoesNotBlockOnUnlimitedRetry は、
// MaxReconnectAttempts=-1 でダイアラーが永久ブロックする状況下でも
// reconnect.Transport.Write() が速やかにエラーを返すことを保証する regression テスト。
func TestReconnectTransport_Write_DoesNotBlockOnUnlimitedRetry(t *testing.T) {
	blockDial := make(chan struct{})
	var dialCount atomic.Int32
	mock := newMockTransport("mock1")

	rt, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			if dialCount.Add(1) == 1 {
				return mock, nil
			}
			<-blockDial
			return nil, errors.New("test: dialer canceled")
		}),
		DialConfig: transport.DialConfig{
			SubConnectionID:   "sub1",
			SuperConnectionID: transport.SuperConnectionID(testSuperConnectionID),
			TransportType:     transport.NegotiationNameWebSocket,
		},
		MaxReconnectAttempts: -1,
		ReconnectInterval:    10 * time.Millisecond,
		HeartbeatInterval:    time.Hour,
		HeartbeatTimeout:     time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		close(blockDial)
		_ = rt.Close()
	})

	require.Eventually(t,
		func() bool { return rt.Status() == reconnect.StatusConnected },
		5*time.Second, 10*time.Millisecond,
	)

	mock.Close()
	require.Eventually(t,
		func() bool { return rt.Status() == reconnect.StatusReconnecting },
		5*time.Second, 10*time.Millisecond,
	)

	done := make(chan error, 1)
	go func() { done <- rt.Write([]byte("test data")) }()

	select {
	case err := <-done:
		require.Error(t, err, "Write should return an error during unlimited-retry reconnect")
	case <-time.After(2 * time.Second):
		t.Fatal("Write blocked >2s during unlimited-retry reconnect")
	}
}

// TestMultiTransport_Write_FallbackDuringUnlimitedRetry は、
// 複数 sub-conn のうち 1 つが MaxReconnectAttempts=-1 でリトライ中・宛先ブロック状態でも
// multi.Transport.Write() が他の健全な sub-conn へフォールバックして無限ブロックしないことを
// 保証する regression テスト。
func TestMultiTransport_Write_FallbackDuringUnlimitedRetry(t *testing.T) {
	blockDial1 := make(chan struct{})
	var dialCount1 atomic.Int32
	mock1 := newMockTransport("mock1")

	rt1, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			if dialCount1.Add(1) == 1 {
				return mock1, nil
			}
			<-blockDial1
			return nil, errors.New("test: dialer canceled")
		}),
		DialConfig: transport.DialConfig{
			SubConnectionID:   "sub1",
			SuperConnectionID: transport.SuperConnectionID(testSuperConnectionID),
			TransportType:     transport.NegotiationNameWebSocket,
		},
		MaxReconnectAttempts: -1,
		ReconnectInterval:    10 * time.Millisecond,
		HeartbeatInterval:    time.Hour,
		HeartbeatTimeout:     time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)

	mock2 := newMockTransport("mock2")
	rt2 := newTestReconnectTransport(t, mock2, "sub2")

	waitForConnected(t, rt1)
	waitForConnected(t, rt2)

	id1 := transport.SubConnectionID("transport1")
	id2 := transport.SubConnectionID("transport2")
	selector := newMockTransportSelector(id1)

	mt, err := NewTransport(TransportConfig{
		TransportMap: TransportMap{
			id1: rt1,
			id2: rt2,
		},
		TransportSelector:   selector,
		Logger:              log.NewNop(),
		StatusCheckInterval: 100 * time.Millisecond,
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		close(blockDial1)
		_ = mt.Close()
		time.Sleep(200 * time.Millisecond)
	})

	// sub1 を Reconnecting 状態で固めた上で multi.Write をかけると、
	// SelectAvailableTransport が sub2 を返すため mock2 に書き込まれる。
	mock1.Close()
	require.Eventually(t,
		func() bool { return rt1.Status() == reconnect.StatusReconnecting },
		5*time.Second, 10*time.Millisecond,
	)

	done := make(chan error, 1)
	go func() { done <- mt.Write([]byte("multi fallback data")) }()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("multi.Transport.Write blocked >2s despite healthy sub2 available")
	}

	select {
	case received := <-mock2.writeCh:
		require.NotEmpty(t, received)
	case <-time.After(time.Second):
		t.Fatal("write did not reach sub2 within 1s")
	}
}

// TestReconnectTransport_Write_FailsAfterFiniteRetriesExhausted は、
// 有限 MaxReconnectAttempts で再接続が全て失敗した後に Write() が
// 永久ポーリングせず速やかにエラーを返すことを保証する regression テスト。
// （Status が Reconnecting のまま固定されると waitForWritable が固まるバグ対策）
func TestReconnectTransport_Write_FailsAfterFiniteRetriesExhausted(t *testing.T) {
	var dialCount atomic.Int32
	mock := newMockTransport("mock1")

	rt, err := reconnect.Dial(reconnect.DialConfig{
		Dialer: transport.DialerFunc(func(dc transport.DialConfig) (transport.Transport, error) {
			if dialCount.Add(1) == 1 {
				return mock, nil
			}
			return nil, errors.New("test: dialer always fails on reconnect")
		}),
		DialConfig: transport.DialConfig{
			SubConnectionID:   "sub1",
			SuperConnectionID: transport.SuperConnectionID(testSuperConnectionID),
			TransportType:     transport.NegotiationNameWebSocket,
		},
		MaxReconnectAttempts: 2,
		ReconnectInterval:    10 * time.Millisecond,
		HeartbeatInterval:    time.Hour,
		HeartbeatTimeout:     time.Hour,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Close() })

	require.Eventually(t,
		func() bool { return rt.Status() == reconnect.StatusConnected },
		5*time.Second, 10*time.Millisecond,
	)

	mock.Close()
	require.Eventually(t,
		func() bool { return rt.Status() == reconnect.StatusDisconnected },
		5*time.Second, 10*time.Millisecond,
		"status should transition to Disconnected after retries are exhausted",
	)

	done := make(chan error, 1)
	go func() { done <- rt.Write([]byte("data")) }()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Write blocked >2s after reconnect retries were exhausted")
	}
}

// TestMultiTransport_Write_DoesNotFallbackOnPartialSendError は、
// sub-conn の下層 Write が部分送信後にエラーを返した場合に、
// multi.Transport が同じペイロードを別 sub-conn に再送しない（重複/破損防止）ことを
// 保証する regression テスト。
func TestMultiTransport_Write_DoesNotFallbackOnPartialSendError(t *testing.T) {
	mock1 := newMockTransport("mock1")
	rt1 := newTestReconnectTransport(t, mock1, "sub1")
	mock2 := newMockTransport("mock2")
	rt2 := newTestReconnectTransport(t, mock2, "sub2")
	waitForConnected(t, rt1)
	waitForConnected(t, rt2)

	id1 := transport.SubConnectionID("transport1")
	id2 := transport.SubConnectionID("transport2")
	selector := newMockTransportSelector(id1)

	mt, err := NewTransport(TransportConfig{
		TransportMap: TransportMap{
			id1: rt1,
			id2: rt2,
		},
		TransportSelector:   selector,
		Logger:              log.NewNop(),
		StatusCheckInterval: 100 * time.Millisecond,
	})
	require.NoError(t, err)
	t.Cleanup(func() { closeAndWait(t, mt) })

	// mock1 を close することで sub1 の reconnect.Transport 内の下層 tr.Write は
	// 「transport closed」エラーを返す（mock の Write は isClosed で error を返す仕様）。
	// これは ErrNotConnected ではない（部分送信相当の扱い）。
	mock1.Close()

	// 直後（Status が StatusConnected のうちに）Write を発行すると、
	// waitForWritable は Connected を見て tr.Write を呼び、その結果エラーが返る。
	// このエラーは ErrNotConnected で包まれていないため、multi は sub2 にフォールバックしない。
	err = mt.Write([]byte("payload"))
	require.Error(t, err, "expected error from sub1 mid-write failure")
	require.False(t, errors.Is(err, reconnect.ErrNotConnected),
		"mid-write error should not be classified as ErrNotConnected")

	// mock2 にペイロードが流れていないことを確認
	select {
	case got := <-mock2.writeCh:
		t.Fatalf("payload unexpectedly resent to sub2: %v", got)
	case <-time.After(100 * time.Millisecond):
		// expected: sub2 には書き込まれない
	}
}
