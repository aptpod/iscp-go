package multi

import (
	"github.com/aptpod/iscp-go/log"
	"github.com/aptpod/iscp-go/transport"
	"github.com/aptpod/iscp-go/transport/reconnect"
)

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

	// SetLogger はロガーを設定します。
	SetLogger(logger log.Logger)
}

// SelectAvailableTransport は指定されたトランスポートが利用可能か確認し、
// 利用不可の場合はフォールバックを実行する共通ロジック。
// 各セレクターのGet()から呼び出される。
//
// 優先順位:
//  1. selectedIDが接続済みならそれを返す
//  2. 他に接続済みのトランスポートがあればそれを返す
//  3. 再接続中/接続中のトランスポートがあればそれを返す
//  4. 利用可能なトランスポートがなければ空文字を返す
func SelectAvailableTransport(
	selectedID transport.TransportID,
	transports TransportMap,
) transport.TransportID {
	// 選択したトランスポートが接続済みか確認
	if tr, exists := transports[selectedID]; exists {
		if tr.Status() == reconnect.StatusConnected {
			return selectedID
		}
	}

	// フォールバック: 接続済みを優先、再接続中を次点
	var reconnectingID transport.TransportID
	for id, tr := range transports {
		switch tr.Status() {
		case reconnect.StatusConnected:
			return id
		case reconnect.StatusReconnecting, reconnect.StatusConnecting:
			if reconnectingID == "" {
				reconnectingID = id
			}
		}
	}

	return reconnectingID
}
