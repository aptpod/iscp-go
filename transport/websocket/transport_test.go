package websocket_test

import (
	"compress/zlib"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/aptpod/iscp-go/transport"
	"github.com/aptpod/iscp-go/transport/compress"
	. "github.com/aptpod/iscp-go/transport/websocket"
	_ "github.com/aptpod/iscp-go/transport/websocket/gorilla"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
	nwebsocket "nhooyr.io/websocket"
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

	for i := 0; i < 100000; i++ {
		require.NoError(t, testee.Write([]byte{1, 2, 3, 4, 5}))
	}

	for i := 0; i < 100000; i++ {
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
			opts := nwebsocket.AcceptOptions{
				InsecureSkipVerify: true,
				CompressionMode:    nwebsocket.CompressionNoContextTakeover,
			}
			wsconn, err := nwebsocket.Accept(w, r, &opts)
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
