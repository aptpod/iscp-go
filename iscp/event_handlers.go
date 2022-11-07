package iscp

import uuid "github.com/google/uuid"

type (
	nopReconnectedEventHandler       struct{}
	nopDisconnectedEventHandler      struct{}
	nopUpstreamResumedEventHandler   struct{}
	nopUpstreamClosedEventHandler    struct{}
	nopDownstreamClosedEventHandler  struct{}
	nopDownstreamResumedEventHandler struct{}
)

func (h nopReconnectedEventHandler) OnReconnected(ev *ReconnectedEvent)                   {}
func (h nopDownstreamResumedEventHandler) OnDownstreamResumed(ev *DownstreamResumedEvent) {}
func (h nopDownstreamClosedEventHandler) OnDownstreamClosed(ev *DownstreamClosedEvent)    {}
func (h nopUpstreamClosedEventHandler) OnUpstreamClosed(ev *UpstreamClosedEvent)          {}
func (h nopUpstreamResumedEventHandler) OnUpstreamResumed(ev *UpstreamResumedEvent)       {}
func (h nopDisconnectedEventHandler) OnDisconnected(ev *DisconnectedEvent)                {}

// ReconnectedEventは再接続完了イベントです。
type ReconnectedEvent struct {
	// 再接続したコネクションの設定
	Config ConnConfig
}

// ReconnectedEventHandlerは、再接続が完了した時のイベントハンドラです。
type ReconnectedEventHandler interface {
	OnReconnected(ev *ReconnectedEvent)
}

// ReconnectedEventHandlerFuncは、ReconnectedEventHandlerの関数です。
type ReconnectedEventHandlerFunc func(ev *ReconnectedEvent)

func (f ReconnectedEventHandlerFunc) OnReconnected(ev *ReconnectedEvent) {
	f(ev)
}

// DisconnectedEventは接続イベントです。
type DisconnectedEvent struct {
	// 切断したコネクションの設定
	Config ConnConfig
}

// DisconnectedEventHandlerは、コネクションが切断されたときのイベントハンドラです。
type DisconnectedEventHandler interface {
	OnDisconnected(ev *DisconnectedEvent)
}

// DisconnectedEventHandlerFuncは、DisconnectedEventHandlerの関数です。
type DisconnectedEventHandlerFunc func(ev *DisconnectedEvent)

func (f DisconnectedEventHandlerFunc) OnDisconnected(ev *DisconnectedEvent) {
	f(ev)
}

// UpstreamClosedEventはアップストリームのクローズイベントです。
type UpstreamClosedEvent struct {
	// 切断したアップストリームの設定
	Config UpstreamConfig
	// 切断したアップストリームの状態
	State UpstreamState
	// 切断に失敗した場合のエラー情報
	Err error
}

// UpstreamClosedEventHandlerは、アップストリームがクローズされたときのイベントハンドラです。
type UpstreamClosedEventHandler interface {
	OnUpstreamClosed(ev *UpstreamClosedEvent)
}

// UpstreamClosedEventHandlerFuncは、UpstreamClosedEventHandlerの関数です。
type UpstreamClosedEventHandlerFunc func(ev *UpstreamClosedEvent)

func (f UpstreamClosedEventHandlerFunc) OnUpstreamClosed(ev *UpstreamClosedEvent) {
	f(ev)
}

// UpstreamResumedEventはアップストリームの再開イベントです。
type UpstreamResumedEvent struct {
	// 再開したアップストリームのID
	ID uuid.UUID
	// 再開したアップストリームの設定
	Config UpstreamConfig
	// 再開したアップストリームの状態
	State UpstreamState
}

// UpstreamResumedEventHandlerは、アップストリームが再開されたときのイベントハンドラです。
type UpstreamResumedEventHandler interface {
	OnUpstreamResumed(ev *UpstreamResumedEvent)
}

// UpstreamResumedEventHandlerFuncは、UpstreamResumedEventHandlerの関数です。
type UpstreamResumedEventHandlerFunc func(ev *UpstreamResumedEvent)

func (f UpstreamResumedEventHandlerFunc) OnUpstreamResumed(ev *UpstreamResumedEvent) {
	f(ev)
}

// DownstreamClosedEventはダウンストリームのクローズイベントです。
type DownstreamClosedEvent struct {
	// 切断したダウンストリームの設定
	Config DownstreamConfig
	// 切断したダウンストリームの状態
	State DownstreamState
	// 切断に失敗した場合のエラー情報
	Err error
}

// DownstreamClosedEventHandlerは、ダウンストリームがクローズされたときのイベントハンドラです。
type DownstreamClosedEventHandler interface {
	OnDownstreamClosed(ev *DownstreamClosedEvent)
}

// DownstreamClosedEventHandlerFuncは、DownstreamClosedEventHandlerの関数です。
type DownstreamClosedEventHandlerFunc func(ev *DownstreamClosedEvent)

func (f DownstreamClosedEventHandlerFunc) OnDownstreamClosed(ev *DownstreamClosedEvent) {
	f(ev)
}

// DownstreamResumedEventはダウンストリームの再開イベントです。
type DownstreamResumedEvent struct {
	// 再開したダウンストリームのID
	ID uuid.UUID
	// 再開したダウンストリームの設定
	Config DownstreamConfig
	// 再開したダウンストリームの状態
	State DownstreamState
}

// DownstreamResumedEventHandlerは、ダウンストリームが再開されたときのイベントハンドラです。
type DownstreamResumedEventHandler interface {
	OnDownstreamResumed(ev *DownstreamResumedEvent)
}

// DownstreamResumedEventHandlerFuncは、DownstreamResumedEventHandlerの関数です。
type DownstreamResumedEventHandlerFunc func(ev *DownstreamResumedEvent)

func (f DownstreamResumedEventHandlerFunc) OnDownstreamResumed(ev *DownstreamResumedEvent) {
	f(ev)
}
