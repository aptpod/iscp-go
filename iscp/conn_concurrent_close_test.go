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
// 実時間依存であり、stressIterations（stress ビルドで 50）をそのまま使うと
// 待ち時間が単純に膨れる。そのため専用の stressIterationsSlow を使う
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

// TestConn_wireConnMu保持中のClose は、OpenUpstream が wireConnMu を握ったまま
// SendUpstreamOpenRequest の Write でブロックしている最中に Conn.Close を呼ぶ。
//
// close（conn.go:598 の wireConnMu.Lock()）は disconnectSendTimeout の対象外で、
// ctx も見ない素の Mutex.Lock() であるため、先行するロック保持者が解放するまで
// Close は無期限にブロックする。これはあるべき姿とのずれだが、本 plan では
// production コードを変更しないため、その事実を測定して記録する。
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
func TestConn_wireConnMu保持中のClose(t *testing.T) {
	defer goleak.VerifyNone(t)
	d := newDialer(transport.NegotiationParams{})
	// readReliableLoop（Connect が go で起動する）が d.ReadWriter を読み出す前に
	// 差し替えを済ませておく。1 回目のシグナルは Connect 自身が送る ConnectRequest
	// の Write に対応し、2 回目が OpenUpstream の SendUpstreamOpenRequest になる。
	sigRW := newWriteSignalReadWriter(d.ReadWriter)
	d.ReadWriter = sigRW
	RegisterDialer(TransportTest, func() transport.Dialer { return d })

	unblockOpen := make(chan struct{})
	srvDone := make(chan struct{})
	go func() {
		defer close(srvDone)
		mockConnectRequestV4(t, d.srv)
		// UpstreamOpenRequest をすぐには読まず、wireConnMu を握ったままの Write が
		// ブロックし続ける状況を作る。
		<-unblockOpen
		upstreamOpenReq := mustRead(t, d.srv, &message.Ping{}, &message.Pong{}).(*message.UpstreamOpenRequest)
		mustWrite(t, d.srv, &message.UpstreamOpenResponse{
			RequestID:             upstreamOpenReq.RequestID,
			AssignedStreamID:      uuid.New(),
			AssignedStreamIDAlias: 1,
			ResultCode:            message.ResultCodeSucceeded,
			ResultString:          "OK",
			DataIDAliases:         map[uint32]*message.DataID{},
		})
		// 以後 Disconnect が来るまで読み捨てる。
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
	<-sigRW.signal // ConnectRequest の Write 分を消費する。

	openDone := make(chan struct{})
	go func() {
		defer close(openDone)
		_, _ = conn.OpenUpstream(context.Background(), "session_id")
	}()

	// OpenUpstream が wireConnMu を取得し SendUpstreamOpenRequest の Write を
	// 呼び出す（サーバーが unblockOpen まで Read しないため、Write は pipe 上で
	// ブロックし続ける）まで確定的に待つ。time.Sleep によるスケジューリング頼みの
	// 同期をやめ、flaky の原因を解消する。
	<-sigRW.signal

	closeDone := make(chan error, 1)
	go func() { closeDone <- conn.Close(context.Background()) }()

	// disconnectSendTimeout（3s）を超えても Close が返らないことを確認する。
	// wireConnMu の取得待ちは disconnectSendTimeout の対象外であり、Lock が
	// 解放されるまで待ち続ける（あるべき姿とのずれ）。
	select {
	case <-closeDone:
		t.Fatal("Conn.Close returned before wireConnMu was released; expected it to block on wireConnMu acquisition (not bounded by disconnectSendTimeout)")
	case <-time.After(4 * time.Second):
		// 期待どおりブロックしている（disconnectSendTimeout を超えても解放されない）。
	}

	close(unblockOpen) // OpenUpstream の Write を解放し、wireConnMu を解放させる。
	<-openDone

	select {
	case err := <-closeDone:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Conn.Close did not return after wireConnMu was released")
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
