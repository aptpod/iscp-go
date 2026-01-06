package coder_test

import (
	"compress/zlib"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	cwebsocket "github.com/coder/websocket"
	cwebwocket "github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/aptpod/iscp-go/errors"
	"github.com/aptpod/iscp-go/transport"
	"github.com/aptpod/iscp-go/transport/compress"
	. "github.com/aptpod/iscp-go/transport/websocket"
	"github.com/aptpod/iscp-go/transport/websocket/coder"
)

// TODO: test suite

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
			wsconn, err := coder.Dial(url, nil)
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
							wsconn, err := coder.Dial(url, nil)
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

	wsconn, err := coder.Dial(url, nil)
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
			opts := cwebwocket.AcceptOptions{
				InsecureSkipVerify: true,
				CompressionMode:    cwebwocket.CompressionNoContextTakeover,
			}
			wsconn, err := cwebwocket.Accept(w, r, &opts)
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

// TestTransport_WriteBlockOnDisconnect は、WebSocket接続が切断された際に
// Writeがブロックするバグを再現するテストです。
//
// 問題の状況:
// - 複数のgoroutineから並行してWriteを実行中
// - サーバー側で接続が突然切断される
// - Readは即座にエラーを返すが、Writeはmutexロック待ちでブロックし続ける
//
// 期待動作: Write/Read共にタイムアウト内にエラーを返すべき
// 現状: Writeがブロックしてテスト失敗 = バグ再現
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
	wsconn, err := coder.Dial(s.URL, nil)
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
	// 現状はここでブロックしてテスト失敗するはず = バグ再現成功
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
					wr := coder.New(wsconn)
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

			wsconn, err := coder.Dial(s.URL, nil)
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
