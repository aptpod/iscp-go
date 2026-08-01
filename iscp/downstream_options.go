package iscp

import (
	"time"

	"github.com/aptpod/iscp-go/v2/message"
)

// DownstreamConfigは、ダウンストリームの設定です。
type DownstreamConfig struct {
	Filters          []*message.DownstreamFilter // ダウンストリームフィルター
	QoS              message.QoS                 // QoS
	ExpiryInterval   time.Duration               // 有効期限
	DataIDs          []*message.DataID           // データIDエイリアス
	AckFlushInterval *time.Duration              // Ackのフラッシュインターバル
	CloseTimeout     *time.Duration              // final Ack flush待ちのタイムアウト（CloseRequest送信やClose全体には適用しない）

	// ダウンストリームがクローズされたときのイベントハンドラ
	ClosedEventHandler DownstreamClosedEventHandler
	// ダウンストリームが再開されたときのイベントハンドラ
	ResumedEventHandler DownstreamResumedEventHandler
	// 空チャンク省略フラグ。trueの場合、StreamChunk内のDataPointGroupが空の時、DownstreamChunkの送信を省略します。
	OmitEmptyChunk bool
}

var defaultDownstreamConfig = DownstreamConfig{
	Filters:             []*message.DownstreamFilter{},
	QoS:                 message.QoSUnreliable,
	ExpiryInterval:      time.Minute,
	DataIDs:             []*message.DataID{},
	AckFlushInterval:    &defaultAckFlushInterval,
	CloseTimeout:        &defaultCloseTimeout,
	ClosedEventHandler:  nopDownstreamClosedEventHandler{},
	ResumedEventHandler: nopDownstreamResumedEventHandler{},
}

// OptionDownstream、ダウンストリームのオプションです。
type DownstreamOption func(conf *DownstreamConfig)

// WithDownstreamQoSは、QoSを設定します。
func WithDownstreamQoS(qos message.QoS) DownstreamOption {
	return func(conf *DownstreamConfig) {
		conf.QoS = qos
	}
}

// WithDownstreamExpiryIntervalは、有効期限を設定します。
func WithDownstreamExpiryInterval(expiry time.Duration) DownstreamOption {
	return func(conf *DownstreamConfig) {
		conf.ExpiryInterval = expiry
	}
}

// WithDownstreamDataIDsは、データIDを設定します。
func WithDownstreamDataIDs(dataIDs []*message.DataID) DownstreamOption {
	return func(conf *DownstreamConfig) {
		conf.DataIDs = dataIDs
	}
}

// WithDownstreamAckFlushIntervalは、Ackのフラッシュインターバルを設定します。
func WithDownstreamAckFlushInterval(ackInterval time.Duration) DownstreamOption {
	return func(conf *DownstreamConfig) {
		conf.AckFlushInterval = &ackInterval
	}
}

// WithDownstreamCloseTimeoutは、final Ack flushの待機時間上限を設定します。
// `0` を指定した場合は graceful close（final Ack の flush 完了）を待ちません。
// CloseRequestの送信やClose全体のタイムアウトには適用されません。
func WithDownstreamCloseTimeout(timeout time.Duration) DownstreamOption {
	return func(conf *DownstreamConfig) {
		conf.CloseTimeout = &timeout
	}
}

// WithDownstreamResumedEventHandlerは、ダウンストリームが再開されたときのイベントハンドラの設定をします。
func WithDownstreamResumedEventHandler(h DownstreamResumedEventHandler) DownstreamOption {
	return func(o *DownstreamConfig) {
		o.ResumedEventHandler = h
	}
}

// WithDownstreamClosedEventHandlerは、ダウンストリームがクローズされたときのイベントハンドラの設定をします。
func WithDownstreamClosedEventHandler(h DownstreamClosedEventHandler) DownstreamOption {
	return func(o *DownstreamConfig) {
		o.ClosedEventHandler = h
	}
}

// WithDownstreamOmitEmptyChunkは、データポイントが空だった場合にDownstreamChunkの送信を省略します。
func WithDownstreamOmitEmptyChunk() DownstreamOption {
	return func(o *DownstreamConfig) {
		o.OmitEmptyChunk = true
	}
}
