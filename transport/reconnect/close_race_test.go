package reconnect_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	iscperrors "github.com/aptpod/iscp-go/errors"
	"github.com/aptpod/iscp-go/log"
	"github.com/aptpod/iscp-go/transport"
	. "github.com/aptpod/iscp-go/transport/reconnect"
	"github.com/aptpod/iscp-go/transport/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubTransport は、Dial に時間がかかる状況を再現するための最小限の transport.Transport 実装です。
type stubTransport struct {
	closed    atomic.Bool
	closeCh   chan struct{}
	closeOnce sync.Once
}

func newStubTransport() *stubTransport {
	return &stubTransport{closeCh: make(chan struct{})}
}

func (s *stubTransport) Read() ([]byte, error) {
	<-s.closeCh
	return nil, io.EOF
}

func (s *stubTransport) Write([]byte) error { return nil }

func (s *stubTransport) Close() error {
	s.closed.Store(true)
	s.closeOnce.Do(func() { close(s.closeCh) })
	return nil
}

func (s *stubTransport) CloseWithStatus(transport.CloseStatus) error {
	return s.Close()
}

func (s *stubTransport) AsUnreliable() (transport.UnreliableTransport, bool) {
	return nil, false
}

func (s *stubTransport) NegotiationParams() transport.NegotiationParams {
	return transport.NegotiationParams{}
}

func (s *stubTransport) Name() transport.Name { return "stub" }

func (s *stubTransport) RxBytesCounterValue() uint64 { return 0 }

func (s *stubTransport) TxBytesCounterValue() uint64 { return 0 }

// TestInitialConnect_CloseDuringDial_ClosesAcquiredTransport は、initialConnect が
// dialer.Dial から transport を受け取る前後で Close が割り込んだ場合に、
// 取得した下層 transport が確実に閉じられることを検証します（B1の回帰テスト）。
//
// 修正前は、r.mu.Lock() の直後に closed を再確認していなかったため、Dial 完了と
// Close が競合すると取得した transport が誰にも閉じられずリークしていました。
func TestInitialConnect_CloseDuringDial_ClosesAcquiredTransport(t *testing.T) {
	dialStarted := make(chan struct{})
	releaseDial := make(chan struct{})
	tr := newStubTransport()

	dialer := transport.DialerFunc(func(c transport.DialConfig) (transport.Transport, error) {
		close(dialStarted)
		<-releaseDial
		return tr, nil
	})

	rt, err := Dial(DialConfig{
		Dialer:               dialer,
		DialConfig:           transport.DialConfig{Address: "stub"},
		MaxReconnectAttempts: 1,
		ReconnectInterval:    time.Millisecond,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)

	// dialer.Dial の実行中（stub transport を確保する前）に Close を割り込ませる。
	<-dialStarted
	require.NoError(t, rt.Close())

	// Dial を完了させ、initialConnect に stub transport を渡す。
	close(releaseDial)

	require.Eventually(t,
		func() bool { return tr.closed.Load() },
		time.Second, 10*time.Millisecond,
		"transport acquired after Close should be closed by initialConnect",
	)
}

// TestReconnect_AfterClose_StatusStaysDisconnected は、Close 済みの Transport に対して
// reconnect が走っても Status() が StatusDisconnected のまま変化しないことを検証します
// （B5の回帰テスト）。
//
// 修正前は、reconnect が closed を確認する前に status を StatusReconnecting へ
// 変更していたため、Close が設定した StatusDisconnected が一時的に上書きされたまま
// 戻らず、Status() が StatusReconnecting を返し続けていました。
func TestReconnect_AfterClose_StatusStaysDisconnected(t *testing.T) {
	sv := httptest.NewServer(http.HandlerFunc(echoHandler(t)))
	t.Cleanup(sv.Close)
	svURL, err := url.Parse(sv.URL)
	require.NoError(t, err)

	rt, err := Dial(DialConfig{
		Dialer: websocket.NewDefaultDialer(),
		DialConfig: transport.DialConfig{
			Address: svURL.Host,
		},
		MaxReconnectAttempts: 5,
		ReconnectInterval:    10 * time.Millisecond,
		Logger:               log.NewNop(),
	})
	require.NoError(t, err)

	require.Eventually(t,
		func() bool { return rt.Status() == StatusConnected },
		time.Second, 10*time.Millisecond,
		"transport should become connected",
	)
	old := rt.CurrentTransport()
	require.NotNil(t, old)

	require.NoError(t, rt.Close())
	assert.Equal(t, StatusDisconnected, rt.Status(), "status should be Disconnected right after Close")

	err = rt.Reconnect(old)
	assert.ErrorIs(t, err, iscperrors.ErrConnectionClosed)
	assert.Equal(t, StatusDisconnected, rt.Status(), "status must stay Disconnected after reconnect races with Close")
}
