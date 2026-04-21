package quic_test

import (
	"compress/zlib"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	quicgo "github.com/quic-go/quic-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aptpod/iscp-go/v2/internal/testdata"
	"github.com/aptpod/iscp-go/v2/transport"
	"github.com/aptpod/iscp-go/v2/transport/compress"
	"github.com/aptpod/iscp-go/v2/transport/metrics"
	. "github.com/aptpod/iscp-go/v2/transport/quic"
)

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

func TestTransport_ReadWrite_LargeData(t *testing.T) {
	url, f := startEchoServer(t)
	tlsconf := testdata.GetTLSConfig()
	tlsconf.NextProtos = []string{"iscp"}
	sess, err := quicgo.DialAddr(context.Background(), url, tlsconf, &quicgo.Config{
		EnableDatagrams:                  true,
		EnableStreamResetPartialDelivery: true,
	})
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}

	testee, _ := New(Config{
		Connection: sess,
	})
	defer testee.Close()

	b := make([]byte, 20000)
	require.NoError(t, testee.Write(b))
	got, err := testee.Read()
	require.NoError(t, err)
	assert.Equal(t, b, got)
	assert.Equal(t, testee.TxBytesCounterValue(), testee.RxBytesCounterValue())
	assert.NotEqual(t, 0, testee.RxBytesCounterValue())
	t.Cleanup(f)
}

func TestTransport_ReadWrite(t *testing.T) {
	url, f := startEchoServer(t)
	t.Cleanup(f)
	compressionLevel := []int{
		0,
		zlib.BestCompression,
		zlib.BestCompression,
		zlib.NoCompression,
		zlib.BestSpeed,
		zlib.BestCompression,
		zlib.DefaultCompression,
		zlib.HuffmanOnly,
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
		{
			name: "large msg",
			inputAndWants: [][]byte{
				genBytes(t, 1200),
				genBytes(t, 1300),
				genBytes(t, 1400),
				genBytes(t, 1500),
				genBytes(t, 1600),
				genBytes(t, 1700),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, cc := range compressionLevel {
				t.Run(childTestNameLevel(cc), func(t *testing.T) {
					tlsconf := testdata.GetTLSConfig()
					tlsconf.NextProtos = []string{"iscp"}
					sess, err := quicgo.DialAddr(context.Background(), url, tlsconf, &quicgo.Config{
						EnableDatagrams:                  true,
						EnableStreamResetPartialDelivery: true,
					})
					if err != nil {
						t.Fatalf("unexpected error %v", err)
					}

					testee, _ := New(Config{
						Connection: sess,
						CompressConfig: compress.Config{
							Enable: true,
							Level:  cc,
						},
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
}

func TestTransport_ReadWrite_Datagrams(t *testing.T) {
	url, f := startEchoServerDatagram(t)
	t.Cleanup(f)
	compressionLevel := []int{
		0,
		zlib.BestCompression,
		zlib.BestCompression,
		zlib.NoCompression,
		zlib.BestSpeed,
		zlib.BestCompression,
		zlib.DefaultCompression,
		zlib.HuffmanOnly,
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
		{
			name: "large msg",
			inputAndWants: [][]byte{
				genBytes(t, 1200),
				genBytes(t, 1300),
				genBytes(t, 1400),
				genBytes(t, 1500),
				genBytes(t, 1600),
				genBytes(t, 1700),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, cc := range compressionLevel {
				t.Run(childTestNameLevel(cc), func(t *testing.T) {
					tlsconf := testdata.GetTLSConfig()
					tlsconf.NextProtos = []string{"iscp"}
					sess, err := quicgo.DialAddr(context.Background(), url, tlsconf, &quicgo.Config{
						EnableDatagrams:                  true,
						EnableStreamResetPartialDelivery: true,
					})
					if err != nil {
						t.Fatalf("unexpected error %v", err)
					}

					tr, _ := New(Config{
						Connection: sess,
						CompressConfig: compress.Config{
							Enable: true,
							Level:  cc,
						},
					})
					defer tr.Close()
					testee, _ := tr.AsUnreliable()

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
}

func genBytes(t *testing.T, size int) []byte {
	return []byte(strings.Repeat("a", size))
}

func childTestNameLevel(level int) string {
	return fmt.Sprintf("level:%v", level)
}

func startEchoServer(t testing.TB) (string, func()) {
	t.Helper()
	tlsConfig := testdata.GetTLSConfig()
	tlsConfig.NextProtos = []string{"iscp"}
	lis, err := quicgo.ListenAddr("localhost:0", tlsConfig, nil)
	require.NoError(t, err)
	quicServerAddress := lis.Addr().String()

	ctx := context.Background()
	go func() {
		for {
			sess, err := lis.Accept(ctx)
			if err != nil {
				if e, ok := err.(net.Error); ok && e.Temporary() {
					continue
				}
				return
			}

			go func() {
				defer sess.CloseWithError(0, "")

				for {
					recvStream, err := sess.AcceptUniStream(ctx)
					if err != nil {
						if e, ok := err.(net.Error); ok && e.Temporary() {
							continue
						}
						return
					}
					sendStream, err := sess.OpenUniStream()
					if err != nil {
						if e, ok := err.(net.Error); ok && e.Temporary() {
							continue
						}
						return
					}

					if _, err := io.Copy(sendStream, recvStream); err != nil {
						return
					}
					if err := sendStream.Close(); err != nil {
						return
					}
				}
			}()
		}
	}()
	return quicServerAddress, func() { lis.Close() }
}

func startEchoServerDatagram(t testing.TB) (string, func()) {
	t.Helper()
	tlsConfig := testdata.GetTLSConfig()
	tlsConfig.NextProtos = []string{"iscp"}
	lis, err := quicgo.ListenAddr("localhost:0", tlsConfig, &quicgo.Config{
		EnableDatagrams:                  true,
		EnableStreamResetPartialDelivery: true,
	})
	require.NoError(t, err)
	quicServerAddress := lis.Addr().String()

	ctx := context.Background()

	go func() {
		for {
			sess, err := lis.Accept(ctx)
			if err != nil {
				if e, ok := err.(net.Error); ok && e.Temporary() {
					continue
				}
				return
			}

			go func() {
				defer sess.CloseWithError(0, "")

				for {
					recvStream, err := sess.AcceptUniStream(ctx)
					if err != nil {
						if e, ok := err.(net.Error); ok && e.Temporary() {
							continue
						}
						return
					}
					sendStream, err := sess.OpenUniStream()
					if err != nil {
						if e, ok := err.(net.Error); ok && e.Temporary() {
							continue
						}
						return
					}

					if _, err := io.Copy(sendStream, recvStream); err != nil {
						return
					}
					if err := sendStream.Close(); err != nil {
						return
					}
				}
			}()

			go func() {
				defer sess.CloseWithError(0, "")

				for {
					msg, err := sess.ReceiveDatagram(ctx)
					if err != nil {
						if e, ok := err.(net.Error); ok && e.Temporary() {
							continue
						}
						return
					}
					if err := sess.SendDatagram(msg); err != nil {
						return
					}
				}
			}()
		}
	}()
	return quicServerAddress, func() { lis.Close() }
}

func TestTransport_AsUnreliable(t *testing.T) {
	tr := &Transport{}
	_, got1 := tr.AsUnreliable()
	assert.True(t, got1)
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
			tlsconf := testdata.GetTLSConfig()
			tlsconf.NextProtos = []string{"iscp"}

			provider := metrics.NewQUICMetricsProvider()
			sess, err := quicgo.DialAddr(context.Background(), url, tlsconf, &quicgo.Config{
				EnableDatagrams:                  true,
				EnableStreamResetPartialDelivery: true,
				Tracer:                           provider.Tracer(),
			})
			require.NoError(t, err)

			testee, err := New(Config{
				Connection:      sess,
				MetricsProvider: provider,
			})
			require.NoError(t, err)
			defer testee.Close()

			// データを送信して qlog MetricsUpdated イベントを誘発
			require.NoError(t, testee.Write([]byte("ping")))
			_, _ = testee.Read()

			ms, ok := any(testee).(transport.MetricsSupporter)
			require.True(t, ok, "Transport should implement MetricsSupporter interface")

			p := ms.MetricsProvider()
			require.NotNil(t, p, "MetricsProvider should not be nil")

			if tt.wantRTT {
				assert.Greater(t, p.RTT().Nanoseconds(), int64(0), "RTT should be greater than 0")
			}
			if tt.wantRTTVar {
				assert.GreaterOrEqual(t, p.RTTVar().Nanoseconds(), int64(0), "RTTVar should be >= 0")
			}
			if tt.wantCWND {
				assert.Greater(t, p.CongestionWindow(), uint64(0), "CongestionWindow should be greater than 0")
			}
			if tt.wantBytesInFlight {
				assert.GreaterOrEqual(t, p.BytesInFlight(), uint64(0), "BytesInFlight should be >= 0")
			}
		})
	}
}

func TestTransport_MetricsProvider_Fallback(t *testing.T) {
	// MetricsProvider を渡さない場合、noop にフォールバックしデフォルト値を返す。
	url, f := startEchoServer(t)
	t.Cleanup(f)

	tlsconf := testdata.GetTLSConfig()
	tlsconf.NextProtos = []string{"iscp"}
	sess, err := quicgo.DialAddr(context.Background(), url, tlsconf, &quicgo.Config{
		EnableDatagrams:                  true,
		EnableStreamResetPartialDelivery: true,
	})
	require.NoError(t, err)

	testee, err := New(Config{
		Connection: sess,
	})
	require.NoError(t, err)
	defer testee.Close()

	p := testee.MetricsProvider()
	require.NotNil(t, p)
	assert.Equal(t, 100*time.Millisecond, p.RTT())
	assert.Equal(t, 50*time.Millisecond, p.RTTVar())
	assert.Equal(t, uint64(14600), p.CongestionWindow())
	assert.Equal(t, uint64(0), p.BytesInFlight())
}

func TestTransport_MetricsProvider_AfterClose(t *testing.T) {
	url, f := startEchoServer(t)
	t.Cleanup(f)

	tlsconf := testdata.GetTLSConfig()
	tlsconf.NextProtos = []string{"iscp"}
	provider := metrics.NewQUICMetricsProvider()
	sess, err := quicgo.DialAddr(context.Background(), url, tlsconf, &quicgo.Config{
		EnableDatagrams:                  true,
		EnableStreamResetPartialDelivery: true,
		Tracer:                           provider.Tracer(),
	})
	require.NoError(t, err)

	testee, err := New(Config{
		Connection:      sess,
		MetricsProvider: provider,
	})
	require.NoError(t, err)

	p := testee.MetricsProvider()
	require.NoError(t, testee.Close())

	// Close 後も getter は panic せずデフォルト値を返す
	assert.NotPanics(t, func() {
		_ = p.RTT()
		_ = p.RTTVar()
		_ = p.CongestionWindow()
		_ = p.BytesInFlight()
	})
}
