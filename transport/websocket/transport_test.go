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

			// Check whether it can be cast to the MetricsSupporter interface
			ms, ok := any(tr).(transport.MetricsSupporter)
			require.True(t, ok, "Transport should implement MetricsSupporter interface")

			// Get the MetricsProvider
			provider := ms.MetricsProvider()
			require.NotNil(t, provider, "MetricsProvider should not be nil")

			// Check whether each metric method can be called
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

// TestTransport_WriteBlockOnDisconnect is a test that reproduces the bug where
// Write blocks when the WebSocket connection is disconnected.
func TestTransport_WriteBlockOnDisconnect(t *testing.T) {
	const (
		writeTimeout = 2 * time.Second
		readTimeout  = 2 * time.Second
	)

	// Server: after accepting a connection, forcibly disconnect without Read
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

		// Wait a little, then forcibly disconnect (without Read)
		time.Sleep(100 * time.Millisecond)
		wsconn.CloseNow()
		close(serverClosed)
	}))
	defer s.Close()

	// Client connection (with a short WriteTimeout)
	wsconn, err := CallDialFunc(s.URL, nil)
	require.NoError(t, err)
	tr := New(Config{
		Conn:         wsconn,
		WriteTimeout: 500 * time.Millisecond, // short timeout for testing
	})
	defer tr.Close()

	// Data to write (of a reasonable size)
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

	// Writer goroutine (continuous writes)
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

	// Wait for the server to disconnect
	<-serverClosed

	// Confirm that Read returns an error within the timeout
	select {
	case err := <-readDone:
		t.Logf("Read returned with error (expected): %v", err)
	case <-time.After(readTimeout):
		t.Fatal("Read blocked - unexpected")
	}

	// Confirm that Write returns within the timeout
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
			// TODO: Sending AbnormalClosure returns an EOF error, and the error code is not propagated. Whether this is by design has not been investigated. Interpreting it as -1 is fine for now, so check and fix as appropriate.
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

// TestDialConfig_UnderlyingConn verifies that a connection created by coderDial always has an UnderlyingConn.
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
			// Call coderDial to establish a WebSocket connection
			conn, err := CallCoderDial(tt.config)
			require.NoError(t, err, "coderDial should succeed")
			defer conn.Close()

			// Get the UnderlyingConn (confirm that it is always non-nil)
			underlyingConn := conn.UnderlyingConn()
			require.NotNil(t, underlyingConn, "UnderlyingConn must not be nil")

			// Confirm that the type is *net.TCPConn
			tcpConn, ok := underlyingConn.(*net.TCPConn)
			assert.True(t, ok, "UnderlyingConn should be *net.TCPConn")
			assert.NotNil(t, tcpConn, "TCPConn should not be nil")
		})
	}
}

// TestTransport_WriteSimple_ReturnsAlreadyClosedOnConnectionClosedError verifies that, in v1
// mode (writeSimple), a Write error caused by the underlying connection is returned
// wrapping transport.ErrAlreadyClosed.
//
// writeSimple used not to wrap the errors from the actual data write (encodeTo) and the Close
// call on the io.WriteCloser obtained with wsconn.Writer (coderHandleError/gorillaHandleError
// wrap only the Writer acquisition stage).
// As a result, even when a real network error was hit by the TOCTOU of reconnect.Transport.writeRaw
// (after waitForWritable() confirms Connected, doReconnect() closes the old transport
// before the actual Write), it could not fall back to multi.Transport.
//
// Close detection over a real network (net.ErrClosed/ECONNRESET, etc.) depends on the TCP
// send buffer size and the kernel's retransmission timing and tends to be flaky, so
// the Conn is mocked to deterministically verify only the error conversion logic.
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
// verifies that, in writeSimple, a transport.ErrAlreadyClosed conversion also happens when
// wr.Close() (rather than wr.Write) returns an error equivalent to a closure.
//
// mockFramedWriteCloser.Close() always returns nil by default, so the above
// TestTransport_WriteSimple_ReturnsAlreadyClosedOnConnectionClosedError only exercised
// the error path of wr.Write, and the isWriteConnectionClosedError conversion branch on the
// wr.Close() side in writeSimple had never been tested.
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

// TestTransport_WriteSimple_ReturnsAlreadyClosedOnWriteTimeout verifies that an error caused by
// a write timeout (the write ctx expires after writeTimeout elapses) is also converted to
// transport.ErrAlreadyClosed.
//
// When the write ctx expires, coder/websocket rewrites the writeFrame error to ctx.Err()
// (= context.DeadlineExceeded) and returns it, and at the same time timeoutLoop closes the
// connection. That is, the connection after a write timeout is actually dead, but because
// isWriteConnectionClosedError looked only at context.Canceled, the bare error leaked upward
// and joined the reconnect path of reconnect.Transport (conversion to ErrNotConnected) one beat
// late. The error chain imitates the actual form that coder wraps with %w
// (failed to write frame: context.DeadlineExceeded).
func TestTransport_WriteSimple_ReturnsAlreadyClosedOnWriteTimeout(t *testing.T) {
	mock := &mockFramedConn{
		failAtIndex: 0,
		failErr:     fmt.Errorf("failed to write frame: %w", context.DeadlineExceeded),
	}
	tr := New(Config{
		Conn:         mock,
		WriteTimeout: 2 * time.Second,
	})
	defer tr.Close()

	err := tr.Write([]byte("data"))
	require.Error(t, err)
	assert.True(t, errors.Is(err, transport.ErrAlreadyClosed),
		"Write should return an error wrapping transport.ErrAlreadyClosed when the write fails with context.DeadlineExceeded (write timeout), got: %v", err)
}

// mockFramedConn is a Conn implementation for testing the Write error handling of
// writeSimple/writeFramed. It counts the Writer calls (in writeFramed = the chunk index)
// and returns an error only from the Write of the call that matches failAtIndex.
type mockFramedConn struct {
	callCount   int
	failAtIndex int
	failErr     error

	// failWriterAtIndex/failWriterErr make the Writer() call itself fail (the stage of
	// obtaining the io.WriteCloser, not wr.Write/wr.Close). It is disabled while
	// failWriterErr is nil (it does not affect tests that use only the existing failAtIndex/failErr).
	failWriterAtIndex int
	failWriterErr     error

	// failCloseAtIndex/failCloseErr make wr.Close() fail only at this index.
	// It is disabled while failCloseErr is nil.
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

// TestTransport_WriteFramed_FirstChunkFailure_ReturnsAlreadyClosed verifies that, in v2
// mode (writeFramed), when the first chunk (index 0, not a single byte sent yet) fails
// with an error equivalent to a closure, an error wrapping transport.ErrAlreadyClosed
// is returned.
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

// TestTransport_WriteFramed_SecondChunkFailure_NotReportedAsAlreadyClosed verifies that,
// in v2 mode (writeFramed), when the second or later chunk fails with an error equivalent
// to a closure, it is **not converted** to transport.ErrAlreadyClosed
// (the first chunk may already have reached the peer, so an unconditional conversion would
// cause duplicate sends through the fallback of multi.Transport).
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

// TestTransport_WriteFramed_FirstChunkWriterFailure_ReturnsAlreadyClosed is a reproduction
// test (filling a mutation gap). When obtaining the Writer() at i == 0 itself fails with a
// closure error, not a single byte has been sent yet (no duplicate occurs even with a fallback;
// the case most to be protected against the TOCTOU), so ErrAlreadyClosed must propagate
// upward as is.
//
// There was no test that verifies this i == 0 case, so even if "i > 0 &&" was removed from the
// Writer() acquisition failure guard of writeFramed (= even if a mutation that drops the
// closure determination at i == 0 was introduced), no test detected it (false green).
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
// verifies that, in v2 mode (writeFramed), when obtaining the Writer() itself for the
// second or later chunk fails (in the implementation, in a form already wrapped with
// transport.ErrAlreadyClosed by gorillaHandleError/coderHandleError), that
// ErrAlreadyClosed does not propagate upward as is.
//
// The errors of wr.Write/wr.Close have a guard that converts to ErrAlreadyClosed only when
// i == 0, but the failure to obtain the Writer() itself lacked this guard, and the
// ErrAlreadyClosed already wrapped by gorillaHandleError/coderHandleError passed through
// unconditionally even at index > 0 (the first chunk may already have reached the peer,
// so this also causes duplicate sends).
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

// TestTransport_WriteFramed_SecondChunkWriterFailure_NormalCloseIsPreserved is a regression
// check that if a Writer() acquisition failure at i > 0 were uniformly re-wrapped with %v for
// everything other than ErrAlreadyClosed, the error classification by transport.IsNormalClose
// would also be lost (because %v cuts the whole error chain). In production, currently this
// classification does not reach the Writer() acquisition path (a normal close is generated
// only on the Reader() path in both coder/gorilla), but it is placed as a regression check
// in case the wrapper starts attaching the classification in the future.
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
// is a regression check that, for the same reason as above, the classification is also
// preserved when a Writer() acquisition failure at i > 0 is context.DeadlineExceeded.
// In production, currently this classification does not reach the Writer() acquisition path
// (coder's mu.lock returns only net.ErrClosed or a ctx error), but it is placed as a
// regression check in case the wrapper starts attaching the classification in the future.
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
