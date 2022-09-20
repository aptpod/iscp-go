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

	"github.com/aptpod/iscp-go/internal/testdata"
	"github.com/aptpod/iscp-go/transport/compress"
	. "github.com/aptpod/iscp-go/transport/webtransport"
	"github.com/lucas-clemente/quic-go/http3"
	webtransgo "github.com/marten-seemann/webtransport-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

var address = "localhost:4433"

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

func TestTransport_ReadWrite_LargeData(t *testing.T) {
	addr, f := startEchoServer(t)
	t.Cleanup(f)
	dialer := &webtransgo.Dialer{
		TLSClientConf: testdata.GetTLSConfig(),
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
					dialer := &webtransgo.Dialer{
						TLSClientConf: testdata.GetTLSConfig(),
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
					dialer := &webtransgo.Dialer{
						TLSClientConf: testdata.GetTLSConfig(),
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

	sv := &webtransgo.Server{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
		H3: http3.Server{
			Addr:      addr,
			TLSConfig: testdata.GetTLSConfig(),
		},
	}
	cleanup = func() {
		sv.Close()
	}
	sv.H3.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := sv.Upgrade(w, r)
		if err != nil {
			http.Error(w, "upgrade failed", 500)
			return
		}
		defer conn.Close()

		ctx := r.Context()
		eg, ctx := errgroup.WithContext(ctx)
		eg.Go(func() error {
			for {
				recvStream, err := conn.AcceptUniStream(ctx)
				if err != nil {
					if e, ok := err.(net.Error); ok && e.Temporary() {
						continue
					}
					return err
				}
				sendStream, err := conn.OpenUniStream()
				if err != nil {
					if e, ok := err.(net.Error); ok && e.Temporary() {
						continue
					}
					return err
				}

				if _, err := io.Copy(sendStream, recvStream); err != nil {
					return err
				}
				if err := sendStream.Close(); err != nil {
					return err
				}
			}
		})
		eg.Go(func() error {
			for {
				msg, err := conn.ReceiveMessage()
				if err != nil {
					if e, ok := err.(net.Error); ok && e.Temporary() {
						continue
					}
					return err
				}
				if err := conn.SendMessage(msg); err != nil {
					return err
				}
			}
		})

		eg.Wait()
	})

	go sv.ListenAndServe()
	return
}

func TestTransport_AsUnreliable(t *testing.T) {
	tr := &Transport{}
	_, got1 := tr.AsUnreliable()
	assert.True(t, got1)
}
