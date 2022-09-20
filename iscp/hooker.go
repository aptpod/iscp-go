package iscp

import uuid "github.com/google/uuid"

// ReceiveAckHookerは、Ack受信時のフックのインターフェースです。
type ReceiveAckHooker interface {
	// HookAfterは、Ackを受信した直後に呼び出されます。
	HookAfter(streamID uuid.UUID, ack UpstreamChunkAck)
}

// ReceiveAckHookerFuncは、ReceiveAckHookerの関数実装です。
type ReceiveAckHookerFunc func(streamID uuid.UUID, ack UpstreamChunkAck)

func (f ReceiveAckHookerFunc) HookAfter(streamID uuid.UUID, ack UpstreamChunkAck) {
	f(streamID, ack)
}

// SendDataPointsHookerは、データ送信時のフックのインターフェースです。
type SendDataPointsHooker interface {
	// HookBeforeは、データを送信する直前に呼び出されます。
	HookBefore(streamID uuid.UUID, chunk UpstreamChunk)
}

// SendDataPointsHookerFuncは、SendDataPointsHookerの関数実装です。
type SendDataPointsHookerFunc func(streamID uuid.UUID, chunk UpstreamChunk)

func (f SendDataPointsHookerFunc) HookBefore(streamID uuid.UUID, chunk UpstreamChunk) {
	f(streamID, chunk)
}
