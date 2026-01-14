package webtransport_test

import (
	"compress/zlib"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aptpod/iscp-go/internal/testdata"
	"github.com/aptpod/iscp-go/transport/compress"
	. "github.com/aptpod/iscp-go/transport/webtransport"
	quic "github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	webtransgo "github.com/quic-go/webtransport-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

func TestTransport_ReadWrite_LargeData(t *testing.T) {
	addr, f := startEchoServer(t)
	t.Cleanup(f)
	tlsClientConfig := testdata.GetTLSConfig()
	tlsClientConfig.NextProtos = []string{"h3"}
	dialer := &webtransgo.Dialer{
		TLSClientConfig: tlsClientConfig,
		QUICConfig: &quic.Config{
			EnableDatagrams: true, EnableStreamResetPartialDelivery: true,
		},
	}
	url := fmt.Sprintf("https://%s", addr)
	_, conn, err := dialer.Dial(context.Background(), url, nil)
	require.NoError(t, err)

	testee, _ := New(Config{
		Connection: conn,
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
	addr, f := startEchoServer(t)
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, cc := range compressionLevel {
				t.Run(childTestNameLevel(cc), func(t *testing.T) {
					tlsClientConfig := testdata.GetTLSConfig()
					tlsClientConfig.NextProtos = []string{"h3"}
					dialer := &webtransgo.Dialer{
						TLSClientConfig: tlsClientConfig,
						QUICConfig:      &quic.Config{EnableDatagrams: true, EnableStreamResetPartialDelivery: true},
					}
					url := fmt.Sprintf("https://%s", addr)
					_, conn, err := dialer.Dial(context.Background(), url, nil)
					require.NoError(t, err)

					testee, _ := New(Config{
						Connection: conn,
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
	addr, f := startEchoServer(t)
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
					tlsClientConfig := testdata.GetTLSConfig()
					tlsClientConfig.NextProtos = []string{"h3"}
					dialer := &webtransgo.Dialer{
						TLSClientConfig: tlsClientConfig,
						QUICConfig:      &quic.Config{EnableDatagrams: true, EnableStreamResetPartialDelivery: true},
					}
					url := fmt.Sprintf("https://%s", addr)
					_, conn, err := dialer.Dial(context.Background(), url, nil)
					require.NoError(t, err)

					testee, _ := New(Config{
						Connection: conn,
						CompressConfig: compress.Config{
							Enable: true,
							Level:  cc,
						},
					})
					defer testee.Close()

					for _, v := range tt.inputAndWants {
						require.NoError(t, testee.WriteUnreliable(v))
					}

					for _, v := range tt.inputAndWants {
						got, err := testee.ReadUnreliable()
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

func startEchoServer(t *testing.T) (addr string, cleanup func()) {
	t.Helper()
	addr = "localhost:4433"

	tlsConfig := testdata.GetTLSConfig()
	tlsConfig.NextProtos = []string{"h3"}
	h3Server := &http3.Server{
		TLSConfig:  tlsConfig,
		QUICConfig: &quic.Config{EnableDatagrams: true, EnableStreamResetPartialDelivery: true},
	}
	webtransgo.ConfigureHTTP3Server(h3Server)
	sv := &webtransgo.Server{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
		H3: h3Server,
	}

	h3Server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := sv.Upgrade(w, r)
		if err != nil {
			http.Error(w, "upgrade failed", 500)
			return
		}

		ctx := r.Context()

		// Stream echo goroutine
		go func() {
			defer conn.CloseWithError(0, "")

			recvStream, err := conn.AcceptUniStream(ctx)
			if err != nil {
				return
			}
			sendStream, err := conn.OpenUniStream()
			if err != nil {
				return
			}
			defer sendStream.Close()

			// io.Copy でストリームをエコー
			io.Copy(sendStream, recvStream)
		}()

		// Datagram echo goroutine
		go func() {
			for {
				msg, err := conn.ReceiveDatagram(ctx)
				if err != nil {
					return
				}
				conn.SendDatagram(msg)
			}
		}()
	})

	// UDP接続を作成してServeを呼び出す
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		t.Fatal(err)
	}

	cleanup = func() {
		sv.Close()
		udpConn.Close()
	}

	go sv.Serve(udpConn)
	// サーバーの起動を待機
	time.Sleep(100 * time.Millisecond)
	return
}

func TestTransport_AsUnreliable(t *testing.T) {
	tr := &Transport{}
	_, got1 := tr.AsUnreliable()
	assert.True(t, got1)
}
