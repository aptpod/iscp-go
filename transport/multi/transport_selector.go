package multi

import "github.com/aptpod/iscp-go/transport"

// TransportSelector は、データサイズに基づいて最適なTransportIDを選択するインターフェース。
// 実装は初期化時にバックグラウンド処理を開始する責任を持つ。
type TransportSelector interface {
	// Get は指定されたデータサイズに基づいて最適なTransportIDを返す。
	// bsSize: 送信するデータのバイト数
	Get(bsSize int64) transport.TransportID
}

// ECFTransportUpdater は、ECFスケジューラのトランスポートメトリクスを更新するインターフェースです。
// ECFSelector などの ECF ベースのセレクタがこのインターフェースを実装します。
type ECFTransportUpdater interface {
	// UpdateTransport は指定されたトランスポートのメトリクス情報を更新します。
	UpdateTransport(transportID transport.TransportID, info *TransportInfo)

	// SetQueueSize は送信待ちキューのサイズを設定します。
	SetQueueSize(queueSize uint64)
}
