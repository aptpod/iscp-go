package iscp

import (
	"time"

	"github.com/aptpod/iscp-go/message"
)

// UpstreamConfigは、アップストリーム設定です。
type UpstreamConfig struct {
	SessionID      string            // セッションID
	AckInterval    *time.Duration    // Ackの返却間隔
	CloseTimeout   *time.Duration    // Close時のタイムアウト
	ExpiryInterval time.Duration     // 有効期限
	DataIDs        []*message.DataID // データIDリスト
	QoS            message.QoS       // QoS
	Persist        bool              // 永続化するかどうか
	FlushPolicy    FlushPolicy
	// Ack受信時のフック
	ReceiveAckHooker ReceiveAckHooker
	// データポイント送信時のフック
	SendDataPointsHooker SendDataPointsHooker

	// アップストリームがクローズされたときのイベントハンドラ
	ClosedEventHandler UpstreamClosedEventHandler
	// アップストリームが再開されたときのイベントハンドラ
	ResumedEventHandler UpstreamResumedEventHandler
}

var defaultUpstreamConfig = UpstreamConfig{
	SessionID:      "",
	AckInterval:    &defaultAckInterval,
	CloseTimeout:   &defaultCloseTimeout,
	ExpiryInterval: defaultExpiryInterval,
	DataIDs:        []*message.DataID{},
	QoS:            message.QoSUnreliable,
	Persist:        false,
	FlushPolicy: &flushPolicyIntervalOrBufferSize{
		BufferPolicy:   &flushPolicyBufferSizeOnly{BufferSize: uint32(defaultFlushBufferSize)},
		IntervalPolicy: &flushPolicyIntervalOnly{Interval: defaultFlushInterval},
	},
	ReceiveAckHooker:     nil,
	SendDataPointsHooker: nil,
	ClosedEventHandler:   nopUpstreamClosedEventHandler{},
	ResumedEventHandler:  nopUpstreamResumedEventHandler{},
}

type (
	UpstreamOption func(conf *UpstreamConfig)
)

// WithUpstreamAckIntervalは、Ackの返却間隔を設定します。
func WithUpstreamAckInterval(ackInterval time.Duration) UpstreamOption {
	return func(opt *UpstreamConfig) {
		opt.AckInterval = &ackInterval
	}
}

// WithUpstreamCloseTimeoutは、Close時のタイムアウトを設定します。
func WithUpstreamCloseTimeout(timeout time.Duration) UpstreamOption {
	return func(opt *UpstreamConfig) {
		opt.CloseTimeout = &timeout
	}
}

// WithUpstreamExpiryIntervalは、有効期限を設定します。
func WithUpstreamExpiryInterval(expiryInterval time.Duration) UpstreamOption {
	return func(opt *UpstreamConfig) {
		opt.ExpiryInterval = expiryInterval
	}
}

// WithUpstreamDataIDsは、データIDリストを設定します。
func WithUpstreamDataIDs(dataIDs []*message.DataID) UpstreamOption {
	return func(opt *UpstreamConfig) {
		opt.DataIDs = dataIDs
	}
}

// WithUpstreamQoSは、QoSを設定します。
func WithUpstreamQoS(qoS message.QoS) UpstreamOption {
	return func(opt *UpstreamConfig) {
		opt.QoS = qoS
	}
}

// WithUpstreamPersistは、永続化するかどうかを設定します。
func WithUpstreamPersist() UpstreamOption {
	return func(opt *UpstreamConfig) {
		opt.Persist = true
	}
}

// WithUpstreamFlushPolicyは、フラッシュポリシーを設定します。
func WithUpstreamFlushPolicy(policy FlushPolicy) UpstreamOption {
	return func(opt *UpstreamConfig) {
		opt.FlushPolicy = policy
	}
}

// WithUpstreamFlushPolicyNoneは、アップストリームの内部バッファをフラッシュしないポリシーを設定します。
//
// このポリシーを指定した場合、内部バッファのフラッシュはUpstreamのFlushメソッドを使用して、明示的にフラッシュを行う必要があります。
func WithUpstreamFlushPolicyNone() UpstreamOption {
	return func(opt *UpstreamConfig) {
		opt.FlushPolicy = &flushPolicyNone{}
	}
}

// WithUpstreamFlushPolicyIntervalOnlyは、アップストリームのデータポイントの内部バッファを時間間隔でフラッシュするポリシーを設定します。
func WithUpstreamFlushPolicyIntervalOnly(interval time.Duration) UpstreamOption {
	return func(opt *UpstreamConfig) {
		opt.FlushPolicy = &flushPolicyIntervalOnly{Interval: interval}
	}
}

// WithUpstreamFlushPolicyIntervalOrBufferSizeは、アップストリームのデータポイントの内部バッファを時間間隔、または指定したバッファサイズを超えた時にフラッシュするポリシーを設定します。
func WithUpstreamFlushPolicyIntervalOrBufferSize(interval time.Duration, bufferSize uint32) UpstreamOption {
	return func(opt *UpstreamConfig) {
		opt.FlushPolicy = &flushPolicyIntervalOrBufferSize{
			BufferPolicy:   &flushPolicyBufferSizeOnly{BufferSize: bufferSize},
			IntervalPolicy: &flushPolicyIntervalOnly{Interval: interval},
		}
	}
}

// WithUpstreamFlushPolicyBufferSizeOnlyは、アップストリームのデータポイントの内部バッファを指定したバッファサイズを超えた時にフラッシュするポリシーを設定します。
func WithUpstreamFlushPolicyBufferSizeOnly(bufferSize uint32) UpstreamOption {
	return func(opt *UpstreamConfig) {
		opt.FlushPolicy = &flushPolicyBufferSizeOnly{BufferSize: bufferSize}
	}
}

// WithUpstreamFlushPolicyImmediatelyは、アップストリームのデータポイントをバッファに書き込んだタイミングで即時フラッシュするポリシーを設定します。
func WithUpstreamFlushPolicyImmediately() UpstreamOption {
	return func(opt *UpstreamConfig) {
		opt.FlushPolicy = &flushPolicyImmediately{}
	}
}

// WithUpstreamReceiveAckHookerは、Ack受信時のフックを設定します。
func WithUpstreamReceiveAckHooker(hooker ReceiveAckHooker) UpstreamOption {
	return func(opt *UpstreamConfig) {
		opt.ReceiveAckHooker = hooker
	}
}

// WithUpstreamSendDataPointsHookerは、データポイント送信時のフックを設定します。
func WithUpstreamSendDataPointsHooker(hooker SendDataPointsHooker) UpstreamOption {
	return func(opt *UpstreamConfig) {
		opt.SendDataPointsHooker = hooker
	}
}

// UpstreamCloseOptionは、Upstreamをクローズする時のオプションです。
type UpstreamCloseOption func(opts *upstreamCloseOptions)

var defaultUpstreamCloseOption = upstreamCloseOptions{
	CloseSession: false,
}

// upstreamCloseOptionsは、Upstreamクローズ時のオプションです。
type upstreamCloseOptions struct {
	CloseSession bool // セッションをクローズするかどうか
}

// WithUpstreamCloseEnableCloseSessionは、Upstreamクローズ時のセッションクローズを無効化します
func WithUpstreamCloseEnableCloseSession() UpstreamCloseOption {
	return func(opts *upstreamCloseOptions) {
		opts.CloseSession = true
	}
}

// WithUpstreamResumedEventHandlerは、アップストリームが再開されたときのイベントハンドラの設定をします。
func WithUpstreamResumedEventHandler(h UpstreamResumedEventHandler) UpstreamOption {
	return func(o *UpstreamConfig) {
		o.ResumedEventHandler = h
	}
}

// WithUpstreamClosedEventHandlerは、アップストリームがクローズされたときのイベントハンドラの設定をします。
func WithUpstreamClosedEventHandler(h UpstreamClosedEventHandler) UpstreamOption {
	return func(o *UpstreamConfig) {
		o.ClosedEventHandler = h
	}
}
