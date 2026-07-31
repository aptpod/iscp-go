package websocket_test

import (
	"compress/zlib"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/aptpod/iscp-go/v2/errors"
	"github.com/aptpod/iscp-go/v2/transport"
	"github.com/aptpod/iscp-go/v2/transport/compress"
	"github.com/aptpod/iscp-go/v2/transport/protocol"
	. "github.com/aptpod/iscp-go/v2/transport/websocket"

	cwebsocket "github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

func BenchmarkRead(b *testing.B) {
	url, f := startEchoServer(b)
	b.Cleanup(f)
	testCases := []struct {
		name string
		msgs [][]byte
	}{
		{
			name: "1",
			msgs: [][]byte{
				{1, 2, 3, 4, 5},
			},
		},
		{
			name: "2",
			msgs: [][]byte{
				{1, 2, 3, 4, 5},
				{1, 2, 3, 4, 5},
			},
		},
		{
			name: "4",
			msgs: [][]byte{
				{1, 2, 3, 4, 5},
				{1, 2, 3, 4, 5},
				{1, 2, 3, 4, 5},
				{1, 2, 3, 4, 5},
			},
		},
	}

	for _, tt := range testCases {
		b.Run(tt.name, func(b *testing.B) {
			wsconn, err := CallDialFunc(url, nil)
			if err != nil {
				b.Fatalf("unexpected error %v", err)
			}
			testee := New(Config{
				Conn: wsconn,
			})
			defer testee.Close()

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for _, msg := range tt.msgs {
					_ = testee.Write(msg)
					_, _ = testee.Read()
				}
			}
		})
	}
}

func TestTransport_ReadWrite(t *testing.T) {
	url, f := startEchoServer(t)
	t.Cleanup(f)
	cfgs := []*compress.Config{
		nil,
		{Level: zlib.BestCompression},
		{Level: zlib.BestCompression},
		{Level: zlib.NoCompression},
		{Level: zlib.BestSpeed},
		{Level: zlib.BestCompression},
		{Level: zlib.DefaultCompression},
		{Level: zlib.HuffmanOnly},
		{Level: zlib.BestCompression, WindowBits: 2048},
		{Level: zlib.BestCompression, WindowBits: 2048},
		{Level: zlib.NoCompression, WindowBits: 2048},
		{Level: zlib.BestSpeed, WindowBits: 2048},
		{Level: zlib.BestCompression, WindowBits: 2048},
		{Level: zlib.DefaultCompression, WindowBits: 2048},
		{Level: zlib.HuffmanOnly, WindowBits: 2048},
	}

	tests := []struct {
		name          string
		inputAndWants [][]byte
	}{
		{
			name: "single msg",
			inputAndWants: [][]byte{
				{1, 2, 3, 4, 5},
			},
		},
		{
			name: "multiple msg",
			inputAndWants: [][]byte{
				{1, 2, 3, 4, 5},
				{2, 2, 3, 4, 5},
				{3, 2, 3, 4, 5},
				{4, 2, 3, 4, 5},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, cc := range cfgs {
				t.Run(childTestNameLevel(cc), func(t *testing.T) {
					for _, v := range []bool{true, false} {
						if cc != nil {
							cc.DisableContextTakeover = v
						}
						t.Run(childTestNameDisableContextOver(cc), func(t *testing.T) {
							wsconn, err := CallDialFunc(url, nil)
							if err != nil {
								t.Fatalf("unexpected error %v", err)
							}
							if cc == nil {
								cc = &compress.Config{
									Enable: false,
								}
							}
							testee := New(Config{
								Conn:           wsconn,
								CompressConfig: *cc,
							})
							defer testee.Close()

							for _, v := range tt.inputAndWants {
								require.NoError(t, testee.Write(v))
							}

							for _, v := range tt.inputAndWants {
								got, err := testee.Read()
								require.NoError(t, err)
								assert.Equal(t, v, got)
							}
							assert.Equal(t, testee.TxBytesCounterValue(), testee.RxBytesCounterValue())
							assert.NotEqual(t, 0, testee.RxBytesCounterValue())
						})
					}
				})
			}
		})
	}
}

func TestTransport_ReadWrite_TooMany(t *testing.T) {
	defer goleak.VerifyNone(t)
	url, f := startEchoServer(t)
	defer f()

	wsconn, err := CallDialFunc(url, nil)
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	testee := New(Config{
		Conn: wsconn,
	})
	defer testee.Close()

	for range 100000 {
		require.NoError(t, testee.Write([]byte{1, 2, 3, 4, 5}))
	}

	for range 100000 {
		got, err := testee.Read()
		require.NoError(t, err)
		assert.Equal(t, []byte{1, 2, 3, 4, 5}, got)
	}

	assert.Equal(t, testee.TxBytesCounterValue(), testee.RxBytesCounterValue())
	assert.NotEqual(t, 0, testee.RxBytesCounterValue())
}

func childTestNameLevel(cc *compress.Config) string {
	if cc == nil {
		return "nil"
	}
	return fmt.Sprintf("level:%v", cc.Level)
}

func childTestNameDisableContextOver(cc *compress.Config) string {
	if cc == nil {
		return "nil"
	}
	return fmt.Sprintf("disable_context_takeover:%v", cc.DisableContextTakeover)
}

func startEchoServer(t testing.TB) (string, func()) {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			opts := cwebsocket.AcceptOptions{
				InsecureSkipVerify: true,
				CompressionMode:    cwebsocket.CompressionNoContextTakeover,
			}
			wsconn, err := cwebsocket.Accept(w, r, &opts)
			if err != nil {
				http.Error(w, "", http.StatusInternalServerError)
				return
			}

			for {
				mType, rd, err := wsconn.Reader(context.Background())
				if err != nil {
					return
				}
				wr, err := wsconn.Writer(context.Background(), mType)
				if err != nil {
					return
				}

				if _, err := io.Copy(wr, rd); err != nil {
					return
				}
				if err := wr.Close(); err != nil {
					return
				}
			}
		},
	))
	return s.URL, s.Close
}

func TestTransport_AsUnreliable(t *testing.T) {
	tests := []struct {
		name  string
		want  transport.UnreliableTransport
		want1 bool
	}{
		{
			name:  "success",
			want:  nil,
			want1: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := &Transport{}
			got, got1 := tr.AsUnreliable()
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.want1, got1)
		})
	}
}

func TestTransport_MetricsProvider(t *testing.T) {
	url, f := startEchoServer(t)
	t.Cleanup(f)

	tests := []struct {
		name              string
		wantRTT           bool
		wantRTTVar        bool
		wantCWND          bool
		wantBytesInFlight bool
	}{
		{
			name:              "success: can retrieve metrics from provider",
			wantRTT:           true,
			wantRTTVar:        true,
			wantCWND:          true,
			wantBytesInFlight: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wsconn, err := CallDialFunc(url, nil)
			require.NoError(t, err)

			tr := New(Config{
				Conn: wsconn,
			})
			defer tr.Close()

			// MetricsSupporterインターフェースにキャスト可能か確認
			ms, ok := any(tr).(transport.MetricsSupporter)
			require.True(t, ok, "Transport should implement MetricsSupporter interface")

			// MetricsProviderを取得
			provider := ms.MetricsProvider()
			require.NotNil(t, provider, "MetricsProvider should not be nil")

			// 各メトリクスメソッドを呼び出し可能か確認
			if tt.wantRTT {
				rtt := provider.RTT()
				assert.Greater(t, rtt.Nanoseconds(), int64(0), "RTT should be greater than 0")
			}

			if tt.wantRTTVar {
				rttVar := provider.RTTVar()
				assert.GreaterOrEqual(t, rttVar.Nanoseconds(), int64(0), "RTTVar should be >= 0")
			}

			if tt.wantCWND {
				cwnd := provider.CongestionWindow()
				assert.Greater(t, cwnd, uint64(0), "CongestionWindow should be greater than 0")
			}

			if tt.wantBytesInFlight {
				bytesInFlight := provider.BytesInFlight()
				assert.GreaterOrEqual(t, bytesInFlight, uint64(0), "BytesInFlight should be >= 0")
			}
		})
	}
}

// TestTransport_WriteBlockOnDisconnect は、WebSocket接続が切断された際に
// Writeがブロックするバグを再現するテストです。
func TestTransport_WriteBlockOnDisconnect(t *testing.T) {
	const (
		writeTimeout = 2 * time.Second
		readTimeout  = 2 * time.Second
	)

	// サーバー: 接続受け入れ後、Readせずに強制切断
	serverClosed := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		opts := cwebsocket.AcceptOptions{
			InsecureSkipVerify: true,
		}
		wsconn, err := cwebsocket.Accept(w, r, &opts)
		if err != nil {
			http.Error(w, "", http.StatusInternalServerError)
			return
		}

		// 少し待機してから強制切断（Readしない）
		time.Sleep(100 * time.Millisecond)
		wsconn.CloseNow()
		close(serverClosed)
	}))
	defer s.Close()

	// クライアント接続（WriteTimeoutを短く設定）
	wsconn, err := CallDialFunc(s.URL, nil)
	require.NoError(t, err)
	tr := New(Config{
		Conn:         wsconn,
		WriteTimeout: 500 * time.Millisecond, // テスト用に短いタイムアウト
	})
	defer tr.Close()

	// 書き込みデータ（ある程度のサイズ）
	largeData := make([]byte, 1024)
	for i := range largeData {
		largeData[i] = byte(i % 256)
	}

	// Reader goroutine
	readDone := make(chan error, 1)
	go func() {
		_, err := tr.Read()
		readDone <- err
	}()

	// Writer goroutine (連続書き込み)
	writeDone := make(chan error, 1)
	go func() {
		for {
			err := tr.Write(largeData)
			if err != nil {
				writeDone <- err
				return
			}
		}
	}()

	// サーバー切断を待機
	<-serverClosed

	// Readがタイムアウト内にエラーを返すことを確認
	select {
	case err := <-readDone:
		t.Logf("Read returned with error (expected): %v", err)
	case <-time.After(readTimeout):
		t.Fatal("Read blocked - unexpected")
	}

	// Writeがタイムアウト内に戻ることを確認
	select {
	case err := <-writeDone:
		t.Logf("Write returned with error (expected after fix): %v", err)
	case <-time.After(writeTimeout):
		t.Fatal("Write blocked - BUG REPRODUCED: Writer is stuck in mutex lock after connection closed")
	}
}

func TestTransport_CloseWithStatus(t *testing.T) {
	tests := []struct {
		name       string
		closeWith  transport.CloseStatus
		wantStatus transport.CloseStatus
	}{
		{
			name:       "normal closure",
			closeWith:  transport.CloseStatusNormal,
			wantStatus: transport.CloseStatusNormal,
		},
		{
			name:      "abnormal closure",
			closeWith: transport.CloseStatusAbnormal,
			// TODO: AbnormalClosure を送信するとEOFエラーが返却され、エラーコードが伝播されない。仕様かどうかは未調査。一旦 -1 の解釈で問題ないので適宜確認修正する。
			wantStatus: transport.CloseStatusInternalError,
		},
		{
			name:       "going away",
			closeWith:  transport.CloseStatusGoingAway,
			wantStatus: transport.CloseStatusGoingAway,
		},
		{
			name:       "internal error",
			closeWith:  transport.CloseStatusInternalError,
			wantStatus: transport.CloseStatusInternalError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errCh := make(chan error, 1)
			s := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) {
					opts := cwebsocket.AcceptOptions{
						InsecureSkipVerify: true,
						CompressionMode:    cwebsocket.CompressionNoContextTakeover,
					}
					wsconn, err := cwebsocket.Accept(w, r, &opts)
					if err != nil {
						http.Error(w, "", http.StatusInternalServerError)
						return
					}
					wr := NewCoderConn(wsconn)
					tr := New(Config{Conn: wr})
					defer tr.Close()
					_, err = tr.Read()
					if err != nil {
						errCh <- err
						return
					}
				},
			))
			defer s.Close()

			wsconn, err := CallDialFunc(s.URL, nil)
			require.NoError(t, err)
			tr := New(Config{Conn: wsconn})
			defer tr.Close()

			err = tr.CloseWithStatus(tt.closeWith)
			require.NoError(t, err)

			got := <-errCh
			gotStatus := transport.GetCloseStatus(got)
			assert.Equal(t, tt.wantStatus, gotStatus, got)
			wrErr := tr.Write([]byte{1, 2, 3, 4, 5})
			assert.ErrorIs(t, wrErr, errors.ErrConnectionClosed)
			_, rdErr := tr.Read()
			assert.ErrorIs(t, rdErr, errors.ErrConnectionClosed)
		})
	}
}

// TestDialConfig_UnderlyingConn は、coderDialで作成した接続が必ずUnderlyingConnを持つことを確認します。
func TestDialConfig_UnderlyingConn(t *testing.T) {
	url, cleanup := startEchoServer(t)
	t.Cleanup(cleanup)

	tests := []struct {
		name   string
		config DialConfig
	}{
		{
			name: "success: default config (no EnableMultipathTCP, no DialContext)",
			config: DialConfig{
				URL: url,
			},
		},
		{
			name: "success: with EnableMultipathTCP",
			config: DialConfig{
				URL:                url,
				EnableMultipathTCP: true,
			},
		},
		{
			name: "success: with custom DialContext",
			config: DialConfig{
				URL: url,
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					dialer := &net.Dialer{}
					return dialer.DialContext(ctx, network, addr)
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// coderDialを呼び出してWebSocket接続を確立
			conn, err := CallCoderDial(tt.config)
			require.NoError(t, err, "coderDial should succeed")
			defer conn.Close()

			// UnderlyingConnを取得（必ず非nilであることを確認）
			underlyingConn := conn.UnderlyingConn()
			require.NotNil(t, underlyingConn, "UnderlyingConn must not be nil")

			// 型が*net.TCPConnであることを確認
			tcpConn, ok := underlyingConn.(*net.TCPConn)
			assert.True(t, ok, "UnderlyingConn should be *net.TCPConn")
			assert.NotNil(t, tcpConn, "TCPConn should not be nil")
		})
	}
}

// TestTransport_WriteSimple_ReturnsAlreadyClosedOnConnectionClosedError は、v1
// モード（writeSimple）で、下層コネクション由来の Write エラーが
// transport.ErrAlreadyClosed をラップして返ることを検証する。
//
// writeSimple は wsconn.Writer で取得した io.WriteCloser への実データ書き込み
// （encodeTo）・Close 呼び出しのエラーを、これまでラップしていなかった
// （coderHandleError/gorillaHandleError は Writer 取得段階のみをラップする）。
// これにより reconnect.Transport.writeRaw の TOCTOU（waitForWritable() で
// Connected を確認した後、実際の Write までの間に doReconnect() が old transport
// を Close する）で real network error を掴んでも multi.Transport へ
// フォールバックできなかった。
//
// 実ネットワーク経由でのクローズ検知（net.ErrClosed/ECONNRESET 等）は TCP
// 送信バッファのサイズやカーネルの再送タイミングに依存し flaky になりやすいため、
// Conn をモック化してエラー変換ロジックのみを決定論的に検証する。
func TestTransport_WriteSimple_ReturnsAlreadyClosedOnConnectionClosedError(t *testing.T) {
	mock := &mockFramedConn{
		failAtIndex: 0,
		failErr:     net.ErrClosed,
	}
	tr := New(Config{
		Conn:         mock,
		WriteTimeout: 2 * time.Second,
	})
	defer tr.Close()

	err := tr.Write([]byte("data"))
	require.Error(t, err)
	assert.True(t, errors.Is(err, transport.ErrAlreadyClosed),
		"Write should return an error wrapping transport.ErrAlreadyClosed when the underlying write fails with a connection-closed error, got: %v", err)
}

// TestTransport_WriteSimple_CloseFailureReturnsAlreadyClosedOnConnectionClosedError
// は、writeSimple で wr.Close()（wr.Write ではなく）が閉塞相当のエラーを返した
// 場合にも transport.ErrAlreadyClosed へ変換されることを検証する（F6）。
//
// mockFramedWriteCloser.Close() は既定で常に nil を返すため、上の
// TestTransport_WriteSimple_ReturnsAlreadyClosedOnConnectionClosedError は
// wr.Write のエラー経路しか踏んでおらず、writeSimple 内の wr.Close() 側の
// isWriteConnectionClosedError 変換ブランチは一度もテストされていなかった。
func TestTransport_WriteSimple_CloseFailureReturnsAlreadyClosedOnConnectionClosedError(t *testing.T) {
	mock := &mockFramedConn{
		failCloseAtIndex: 0,
		failCloseErr:     net.ErrClosed,
	}
	tr := New(Config{
		Conn:         mock,
		WriteTimeout: 2 * time.Second,
	})
	defer tr.Close()

	err := tr.Write([]byte("data"))
	require.Error(t, err)
	assert.True(t, errors.Is(err, transport.ErrAlreadyClosed),
		"Write should return an error wrapping transport.ErrAlreadyClosed when wr.Close() fails with a connection-closed error, got: %v", err)
}

// mockFramedConn は writeSimple/writeFramed の Write エラーハンドリングをテスト
// するための Conn 実装。Writer 呼び出し回数（writeFramed では = チャンク index）
// をカウントし、failAtIndex と一致する呼び出しの Write でのみエラーを返す。
type mockFramedConn struct {
	callCount   int
	failAtIndex int
	failErr     error

	// failWriterAtIndex/failWriterErr は Writer() 呼び出し自体（wr.Write/
	// wr.Close ではなく io.WriteCloser の取得段階）を失敗させる。failWriterErr
	// が nil の間は無効（既存の failAtIndex/failErr のみを使うテストに影響しない）。
	failWriterAtIndex int
	failWriterErr     error

	// failCloseAtIndex/failCloseErr は wr.Close() をこの index でのみ
	// 失敗させる。failCloseErr が nil の間は無効。
	failCloseAtIndex int
	failCloseErr     error
}

func (m *mockFramedConn) Close() error                                { return nil }
func (m *mockFramedConn) CloseWithStatus(transport.CloseStatus) error { return nil }
func (m *mockFramedConn) Ping(context.Context) error                  { return nil }
func (m *mockFramedConn) Reader(context.Context) (MessageType, io.Reader, error) {
	return 0, nil, io.EOF
}
func (m *mockFramedConn) UnderlyingConn() net.Conn   { return nil }
func (m *mockFramedConn) SetUnderlyingConn(net.Conn) {}

func (m *mockFramedConn) Writer(ctx context.Context, tp MessageType) (io.WriteCloser, error) {
	idx := m.callCount
	m.callCount++
	if m.failWriterErr != nil && idx == m.failWriterAtIndex {
		return nil, m.failWriterErr
	}
	return &mockFramedWriteCloser{idx: idx, mock: m}, nil
}

type mockFramedWriteCloser struct {
	idx  int
	mock *mockFramedConn
}

func (w *mockFramedWriteCloser) Write(p []byte) (int, error) {
	if w.idx == w.mock.failAtIndex {
		return 0, w.mock.failErr
	}
	return len(p), nil
}

func (w *mockFramedWriteCloser) Close() error {
	if w.mock.failCloseErr != nil && w.idx == w.mock.failCloseAtIndex {
		return w.mock.failCloseErr
	}
	return nil
}

// TestTransport_WriteFramed_FirstChunkFailure_ReturnsAlreadyClosed は、v2
// モード（writeFramed）で最初のチャンク（index 0、まだ 1 バイトも送信して
// いない）が閉塞相当のエラーで失敗した場合、transport.ErrAlreadyClosed を
// ラップしたエラーが返ることを検証する。
func TestTransport_WriteFramed_FirstChunkFailure_ReturnsAlreadyClosed(t *testing.T) {
	mock := &mockFramedConn{
		failAtIndex: 0,
		failErr:     net.ErrClosed,
	}
	tr := New(Config{
		Conn:              mock,
		UseMessageFraming: true,
		WriteTimeout:      2 * time.Second,
	})
	defer tr.Close()

	largeData := make([]byte, protocol.DefaultMaxChunkSize*3)
	err := tr.Write(largeData)
	require.Error(t, err)
	assert.True(t, errors.Is(err, transport.ErrAlreadyClosed),
		"first-chunk failure should be reported as ErrAlreadyClosed (fallback-safe)")
}

// TestTransport_WriteFramed_SecondChunkFailure_NotReportedAsAlreadyClosed は、
// v2 モード（writeFramed）で 2 個目以降のチャンクが閉塞相当のエラーで失敗した
// 場合、transport.ErrAlreadyClosed に**変換されない**ことを検証する
// （1 個目のチャンクは既に相手に届いている可能性があるため、無条件の変換は
// multi.Transport の fallback による重複送信を招く）。
func TestTransport_WriteFramed_SecondChunkFailure_NotReportedAsAlreadyClosed(t *testing.T) {
	mock := &mockFramedConn{
		failAtIndex: 1,
		failErr:     net.ErrClosed,
	}
	tr := New(Config{
		Conn:              mock,
		UseMessageFraming: true,
		WriteTimeout:      2 * time.Second,
	})
	defer tr.Close()

	largeData := make([]byte, protocol.DefaultMaxChunkSize*3)
	err := tr.Write(largeData)
	require.Error(t, err)
	assert.False(t, errors.Is(err, transport.ErrAlreadyClosed),
		"second-chunk (and later) failure must NOT be reported as ErrAlreadyClosed to avoid duplicate resend via fallback")
}

// TestTransport_WriteFramed_FirstChunkWriterFailure_ReturnsAlreadyClosed は
// N6 の再現テスト（ミューテーション穴埋め）。i == 0 の Writer() 取得自体が
// 閉塞エラーで失敗した場合、まだ 1 バイトも送信していない（fallback しても
// 重複が起きない、TOCTOU 対策で最も守りたいケース）ため、ErrAlreadyClosed が
// そのまま上位へ伝播しなければならない。
//
// この i == 0 のケースを検証するテストが存在しなかったため、writeFramed の
// Writer() 取得失敗ガードから「i > 0 &&」を外しても（＝ i == 0 でも閉塞判定を
// 落としてしまう変異を入れても）どのテストも検出できなかった（false green）。
func TestTransport_WriteFramed_FirstChunkWriterFailure_ReturnsAlreadyClosed(t *testing.T) {
	mock := &mockFramedConn{
		failWriterAtIndex: 0,
		failWriterErr:     fmt.Errorf("failed to write control message %+v: %w", net.ErrClosed, transport.ErrAlreadyClosed),
	}
	tr := New(Config{
		Conn:              mock,
		UseMessageFraming: true,
		WriteTimeout:      2 * time.Second,
	})
	defer tr.Close()

	err := tr.Write(make([]byte, protocol.DefaultMaxChunkSize*3))
	require.Error(t, err)
	assert.True(t, errors.Is(err, transport.ErrAlreadyClosed),
		"first-chunk Writer() failure must remain fallback-safe (nothing has been sent yet)")
}

// TestTransport_WriteFramed_SecondChunkWriterFailure_NotReportedAsAlreadyClosed
// は、v2 モード（writeFramed）で 2 個目以降のチャンクの Writer() 取得自体が
// （実装では gorillaHandleError/coderHandleError によって既に
// transport.ErrAlreadyClosed でラップされた形で）失敗した場合、その
// ErrAlreadyClosed が上位へそのまま伝播しないことを検証する（F2）。
//
// wr.Write/wr.Close のエラーには i == 0 の場合に限り ErrAlreadyClosed へ
// 変換するガードがあるが、Writer() 取得自体の失敗にはこのガードがなく、
// gorillaHandleError/coderHandleError が既にラップ済みの ErrAlreadyClosed が
// index > 0 でも無条件に素通りしていた（1 個目は既に相手に届いている可能性が
// あるため、これも重複送信を招く）。
func TestTransport_WriteFramed_SecondChunkWriterFailure_NotReportedAsAlreadyClosed(t *testing.T) {
	mock := &mockFramedConn{
		failWriterAtIndex: 1,
		failWriterErr:     fmt.Errorf("failed to write control message %+v: %w", net.ErrClosed, transport.ErrAlreadyClosed),
	}
	tr := New(Config{
		Conn:              mock,
		UseMessageFraming: true,
		WriteTimeout:      2 * time.Second,
	})
	defer tr.Close()

	largeData := make([]byte, protocol.DefaultMaxChunkSize*3)
	err := tr.Write(largeData)
	require.Error(t, err)
	assert.False(t, errors.Is(err, transport.ErrAlreadyClosed),
		"second-chunk (and later) Writer() failure must NOT be reported as ErrAlreadyClosed to avoid duplicate resend via fallback")
}

// TestTransport_WriteFramed_SecondChunkWriterFailure_NormalCloseIsPreserved は
// i > 0 の Writer() 取得失敗を ErrAlreadyClosed 以外も一律で %v 包み直しすると、
// transport.IsNormalClose によるエラー分類まで失われてしまう（%v はエラー
// チェーン全体を切るため）ことの回帰検出。production では現状この分類は
// Writer() 取得経路に届かないが（coder/gorilla とも正常クローズは Reader()
// 経路でしか生成されない）、将来 wrapper 側が分類を付けるようになった場合の
// 回帰検出として置く。
func TestTransport_WriteFramed_SecondChunkWriterFailure_NormalCloseIsPreserved(t *testing.T) {
	mock := &mockFramedConn{
		failWriterAtIndex: 1,
		failWriterErr:     fmt.Errorf("normal close: %w", errors.ErrConnectionNormalClose),
	}
	tr := New(Config{
		Conn:              mock,
		UseMessageFraming: true,
		WriteTimeout:      2 * time.Second,
	})
	defer tr.Close()

	largeData := make([]byte, protocol.DefaultMaxChunkSize*3)
	err := tr.Write(largeData)
	require.Error(t, err)
	assert.True(t, transport.IsNormalClose(err),
		"second-chunk (and later) Writer() failure classified as normal close must remain detectable as IsNormalClose")
}

// TestTransport_WriteFramed_SecondChunkWriterFailure_DeadlineExceededIsPreserved
// は上記と同じ理由で、i > 0 の Writer() 取得失敗が context.DeadlineExceeded の
// 場合もその分類が保たれることの回帰検出。production では現状この分類は
// Writer() 取得経路に届かない（coder は mu.lock が net.ErrClosed か ctx エラー
// しか返さない）が、将来 wrapper 側が分類を付けるようになった場合の回帰検出
// として置く。
func TestTransport_WriteFramed_SecondChunkWriterFailure_DeadlineExceededIsPreserved(t *testing.T) {
	mock := &mockFramedConn{
		failWriterAtIndex: 1,
		failWriterErr:     fmt.Errorf("deadline: %w", context.DeadlineExceeded),
	}
	tr := New(Config{
		Conn:              mock,
		UseMessageFraming: true,
		WriteTimeout:      2 * time.Second,
	})
	defer tr.Close()

	largeData := make([]byte, protocol.DefaultMaxChunkSize*3)
	err := tr.Write(largeData)
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.DeadlineExceeded),
		"second-chunk (and later) Writer() failure classified as DeadlineExceeded must remain detectable")
}
