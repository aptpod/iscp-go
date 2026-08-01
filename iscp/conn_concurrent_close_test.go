package iscp_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	. "github.com/aptpod/iscp-go/v2/iscp"
	"github.com/aptpod/iscp-go/v2/message"
	"github.com/aptpod/iscp-go/v2/transport"
)

// TestConn_並行Close は stressGoroutines 本の goroutine から同時に Conn.Close(ctx) を
// 呼んでも、全て有限時間で返りパニックしないことを検証する。
//
// close（conn.go:592-626）は state.Swap で saveAndClearAll* だけをガードし、
// SendDisconnect / wireConn.Close() は wireConnMu で直列化されるだけで毎回実行される。
// 2 回目以降の呼び出しでも安全に返ることを、繰り返しで確認する。
func TestConn_並行Close(t *testing.T) {
	defer goleak.VerifyNone(t)
	d := newDialer(transport.NegotiationParams{})
	RegisterDialer(TransportTest, func() transport.Dialer { return d })

	srvDone := make(chan struct{})
	go func() {
		defer close(srvDone)
		mockConnectRequest(t, d.srv)
		// 以後は Disconnect（複数回来うる）を読み捨てる。サーバーは以後何も書かない。
		for {
			if _, err := d.srv.ReadMessage(); err != nil {
				return
			}
		}
	}()

	conn, err := Connect("dummy", TransportTest)
	require.NoError(t, err)

	var wg sync.WaitGroup
	for i := 0; i < stressGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			assert.NotPanics(t, func() {
				_ = conn.Close(context.Background()) // パニックしないことのみ要求。エラーの有無は問わない。
			})
		}()
	}

	allDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(allDone)
	}()

	select {
	case <-allDone:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent Conn.Close did not return within timeout")
	}
	<-srvDone
}

// TestConn_Close_TimesOutWhenDisconnectSendBlocks_繰り返し は既存の
// TestConn_Close_TimesOutWhenDisconnectSendBlocks（conn_test.go:500）を
// stressIterationsSlow 回繰り返す。毎回 disconnectSendTimeout（3s）ぶん待つため
// 実時間依存であり、他パッケージ並みの回数（stress ビルドで 50 回程度）をそのまま
// 使うと待ち時間が単純に膨れる。そのため専用の stressIterationsSlow を使う
// （stress_params_*.go 参照）。
func TestConn_Close_TimesOutWhenDisconnectSendBlocks_繰り返し(t *testing.T) {
	for i := 0; i < stressIterationsSlow; i++ {
		func() {
			defer goleak.VerifyNone(t)
			d := newDialer(transport.NegotiationParams{})
			RegisterDialer(TransportTest, func() transport.Dialer { return d })

			go func() {
				mockConnectRequest(t, d.srv)
				// 以後は一切 Read しない。Disconnect 送信の Write は下層 pipe で
				// 相手が読むまでブロックし続ける。
			}()

			conn, err := Connect("dummy", TransportTest)
			require.NoError(t, err)

			done := make(chan error, 1)
			go func() { done <- conn.Close(context.Background()) }()

			select {
			case err := <-done:
				assert.NoError(t, err, "iteration %d: wireConn.Close() の結果がそのまま返るはず", i)
			case <-time.After(5 * time.Second):
				t.Fatalf("iteration %d: Conn.Close did not return within disconnectSendTimeout + margin", i)
			}
		}()
	}
}

// writeSignalReadWriter は transport.ReadWriter をラップし、Write が呼ばれる
// （= 送信メッセージが下層 pipe に渡ろうとする）たびに non-blocking でシグナルを
// 送る。pipe は unbuffered channel によるランデブー方式なので、Write 呼び出しの
// 中で相手が Read するまでブロックする。シグナルは Write 呼び出しの直前に送る
// ため、受信側は「Write が（その後ブロックするかどうかによらず）呼ばれたこと」を
// 確定的に検知できる。
//
// d.ReadWriter への差し替えは、必ず Connect（＝ runWire/readReliableLoop の
// 起動）より前に行うこと。起動後に差し替えると、readReliableLoop が保持する
// d.ReadWriter の読み出しとフィールド代入がデータレースになる（-race で検出
// 済み）。差し替えを Connect 前に一本化する代わりに、シグナルは one-shot ではなく
// 複数回送れるようにし、呼び出し側で「何回目の Write か」を数えて必要な回数だけ
// 受信する。
type writeSignalReadWriter struct {
	transport.ReadWriter
	signal chan struct{}
}

func newWriteSignalReadWriter(rw transport.ReadWriter) *writeSignalReadWriter {
	return &writeSignalReadWriter{ReadWriter: rw, signal: make(chan struct{}, 8)}
}

func (w *writeSignalReadWriter) Write(b []byte) error {
	select {
	case w.signal <- struct{}{}:
	default:
	}
	return w.ReadWriter.Write(b)
}

// TestConn_OpenUpstreamのWriteブロック中でもCloseがタイムアウトする は、
// OpenUpstream が SendUpstreamOpenRequest の Write でブロックしている最中に
// Conn.Close を呼んでも、disconnectSendTimeout（3s）+ マージン以内に Close が
// 返ることを検証する。
//
// OpenUpstream はスナップショットしたセッションに対してロック外でラウンド
// トリップするため、Write がブロックしても wireConnMu は保持されない。Close は
// ロックを直ちに取得して Disconnect を送信するが、同じ pipe 上で相手が Read
// しないため Disconnect の Write もブロックし、select の disconnectSendTimeout
// で打ち切って wireConn.Close() する。この Close は OpenUpstream 側でブロック
// していた Write と並行して呼ばれるが、安全であることは下層 transport
// （websocket-gorilla/coder, quic, webtransport）それぞれの Write/Close 並行
// 安全性を個別に確認済み
// （例: gorilla/websocket は "The Close ... method can be called concurrently
// with all other methods." と公式ドキュメントに明記。coder/websocket は
// Write 側が内部で closed チャネルを監視し Close で解放される設計。quic-go の
// SendStream.Write は mutex 保護でデータレースなし）。
//
// wireConn.Close() の結果、OpenUpstream 側でブロックしていた Write は下層
// pipe の Close で解放されエラーを返し、SendUpstreamOpenRequest ひいては
// OpenUpstream 自体もエラーで終了する。
//
// 「OpenUpstream が wireConnMu を握った状態」を time.Sleep で作ろうとすると、
// OpenUpstream 側の goroutine が Write 呼び出しに到達する前に Close 側が先に
// wireConnMu を取得してしまうレースが（特に -race やシステム負荷が高い状況で）
// 稀に発生し flaky になる（sleep を 50ms→300ms→1s と伸ばしても解消しなかった）。
// そのため d.ReadWriter を Write シグナル付きのラッパーに差し替え、OpenUpstream
// の Write 呼び出しを確定的に待ってから Close を呼ぶ。
//
// プロトコルバージョンは 4.0.0（mockConnectRequestV4）を使う。3.0.0 だと
// needsPingPong() が true になり keepAliveLoop が Connect 直後（ticker 待ちなし）
// に Ping を送信するため、writeSignalReadWriter のシグナルが OpenUpstream の
// Write ではなく Ping の Write で誤発火してしまう（実際に発生し Close が
// wireConnMu 解放前に返ってしまう flaky の原因になっていた）。
func TestConn_OpenUpstreamのWriteブロック中でもCloseがタイムアウトする(t *testing.T) {
	defer goleak.VerifyNone(t)
	d := newDialer(transport.NegotiationParams{})
	// readReliableLoop（Connect が go で起動する）が d.ReadWriter を読み出す前に
	// 差し替えを済ませておく。1 回目のシグナルは Connect 自身が送る ConnectRequest
	// の Write に対応し、2 回目が OpenUpstream の SendUpstreamOpenRequest になる。
	sigRW := newWriteSignalReadWriter(d.ReadWriter)
	d.ReadWriter = sigRW
	RegisterDialer(TransportTest, func() transport.Dialer { return d })

	srvDone := make(chan struct{})
	go func() {
		defer close(srvDone)
		mockConnectRequestV4(t, d.srv)
		// UpstreamOpenRequest は wireConn.Close() による下層 pipe の Close で
		// Write 側がブロック解除されるため、相手に届く前にエラーになり送られて
		// こない。以後は Read エラー（pipe が Close された）まで読み捨てる。
		for {
			if _, err := d.srv.ReadMessage(); err != nil {
				return
			}
		}
	}()

	conn, err := Connect("dummy", TransportTest)
	require.NoError(t, err)
	<-sigRW.signal // ConnectRequest の Write 分を消費する。

	openDone := make(chan struct{})
	go func() {
		defer close(openDone)
		_, _ = conn.OpenUpstream(context.Background(), "session_id")
	}()

	// OpenUpstream が SendUpstreamOpenRequest の Write を呼び出す（サーバーが
	// Read しないため、Write は pipe 上でブロックし続ける）まで確定的に待つ。
	// time.Sleep によるスケジューリング頼みの同期をやめ、flaky の原因を解消する。
	<-sigRW.signal

	closeStart := time.Now()
	closeDone := make(chan error, 1)
	go func() { closeDone <- conn.Close(context.Background()) }()

	// disconnectSendTimeout（既定 3s）+ 余裕を見て 5s 以内に返ることを期待する。
	// このシナリオでは Disconnect 送信の待ちだけがタイムアウトの対象になり、
	// 1 回分の disconnectSendTimeout で Close が返るはず。
	select {
	case err := <-closeDone:
		assert.NoError(t, err, "wireConn.Close() の結果がそのまま返るはず")
		assert.Less(t, time.Since(closeStart), 5*time.Second,
			"Close should return within disconnectSendTimeout + margin even while a Write is blocked")
	case <-time.After(5 * time.Second):
		t.Fatal("Conn.Close did not return within disconnectSendTimeout + margin while a Write was blocked")
	}

	select {
	case <-openDone:
	case <-time.After(5 * time.Second):
		t.Fatal("OpenUpstream did not return after wireConn.Close() released the underlying pipe")
	}
	<-srvDone
}

// TestConn_Write中にClose は Upstream.WriteDataPoints を継続的に呼びながら
// Conn.Close を呼んでも、有限時間で返ることを検証する。
//
// 既存の TestUpstream_ClientConnClose（upstream_test.go:1253）は
// time.After(100µs) を挟んで Close と WriteDataPoints のレースを意図的に
// 避けているが（コード中の TODO 参照）、本テストは繰り返しでそのレースを
// 起こしにいく。
func TestConn_Write中にClose(t *testing.T) {
	defer goleak.VerifyNone(t)
	d := newDialer(transport.NegotiationParams{})
	RegisterDialer(TransportTest, func() transport.Dialer { return d })

	srvDone := make(chan struct{})
	go func() {
		defer close(srvDone)
		mockConnectRequest(t, d.srv)
		upstreamOpenReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamOpenRequest)
		mustWrite(t, d.srv, &message.UpstreamOpenResponse{
			RequestID:             upstreamOpenReq.RequestID,
			AssignedStreamID:      uuid.New(),
			AssignedStreamIDAlias: 1,
			ResultCode:            message.ResultCodeSucceeded,
			ResultString:          "OK",
			DataIDAliases:         map[uint32]*message.DataID{},
		})
		// 以後 Disconnect が来るまで chunk 等を読み捨てる。
		for {
			msg, err := d.srv.ReadMessage()
			if err != nil {
				return
			}
			if _, ok := msg.(*message.Disconnect); ok {
				return
			}
		}
	}()

	conn, err := Connect("dummy", TransportTest)
	require.NoError(t, err)

	up, err := conn.OpenUpstream(context.Background(), "session_id",
		WithUpstreamQoS(message.QoSUnreliable),
	)
	require.NoError(t, err)

	dataID := &message.DataID{Name: "name", Type: "type"}
	stopWrite := make(chan struct{})
	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		for {
			select {
			case <-stopWrite:
				return
			default:
				_ = up.WriteDataPoints(context.Background(), dataID, &message.DataPoint{
					ElapsedTime: time.Second,
					Payload:     []byte{1, 2, 3, 4},
				})
			}
		}
	}()

	closeDone := make(chan error, 1)
	go func() { closeDone <- conn.Close(context.Background()) }()

	select {
	case err := <-closeDone:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Conn.Close did not return within timeout while WriteDataPoints loop is running")
	}

	close(stopWrite)
	<-writeDone
	<-srvDone
}
