package iscp

import (
	"time"

	"github.com/aptpod/iscp-go/message"
)

// DownstreamConfigは、ダウンストリームの設定です。
type DownstreamConfig struct {
	Filters        []*message.DownstreamFilter // ダウンストリームフィルター
	QoS            message.QoS                 // QoS
	ExpiryInterval time.Duration               // 有効期限
	DataIDAliases  []*message.DataID           // データIDエイリアス
	AckInterval    *time.Duration              // Ackのインターバル

	// ダウンストリームがクローズされたときのイベントハンドラ
	ClosedEventHandler DownstreamClosedEventHandler
	// ダウンストリームが再開されたときのイベントハンドラ
	ResumedEventHandler DownstreamResumedEventHandler
}

var defaultDownstreamConfig = DownstreamConfig{
	Filters:             []*message.DownstreamFilter{},
	QoS:                 message.QoSUnreliable,
	ExpiryInterval:      time.Minute,
	DataIDAliases:       []*message.DataID{},
	AckInterval:         &defaultAckFlushInterval,
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
		conf.DataIDAliases = dataIDs
	}
}

// WithDownstreamAckIntervalは、Ackのインターバルを設定します。
func WithDownstreamAckInterval(ackInterval time.Duration) DownstreamOption {
	return func(conf *DownstreamConfig) {
		conf.AckInterval = &ackInterval
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
