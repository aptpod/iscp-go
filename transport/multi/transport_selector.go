package multi

import (
	"context"

	"github.com/aptpod/iscp-go/v2/log"
	"github.com/aptpod/iscp-go/v2/transport"
	"github.com/aptpod/iscp-go/v2/transport/reconnect"
)

// TransportSelector は、データサイズに基づいて最適なSubConnectionIDを選択するインターフェース。
// 実装は初期化時にバックグラウンド処理を開始する責任を持つ。
type TransportSelector interface {
	// Get は指定されたデータサイズに基づいて最適なSubConnectionIDを返す。
	// ctx: キャンセル可能なコンテキスト
	// bsSize: 送信するデータのバイト数
	Get(ctx context.Context, bsSize int64) transport.SubConnectionID
}

// TransportMetricsUpdater は、メトリクスベースのトランスポートセレクタのための共通インターフェースです。
// ECFSelector や MinRTTSelector などのメトリクスを使用するセレクタがこのインターフェースを実装します。
type TransportMetricsUpdater interface {
	// UpdateTransport は指定されたトランスポートのメトリクス情報を更新します。
	UpdateTransport(transportID transport.SubConnectionID, info *TransportInfo)

	// SetQueueSize は送信待ちキューのサイズを設定します。
	// ECFSelector では不等式計算に使用され、MinRTTSelector では no-op となります。
	SetQueueSize(queueSize uint64)

	// SetLogger はロガーを設定します。
	SetLogger(logger log.Logger)
}

// ECFTransportUpdater は後方互換性のためのエイリアスです。
// Deprecated: TransportMetricsUpdater を使用してください。
type ECFTransportUpdater = TransportMetricsUpdater

// StatusFunc は SubConnectionID のステータスを返す関数型。
type StatusFunc func(transport.SubConnectionID) reconnect.Status

// SelectAvailableTransportFunc は SelectAvailableTransport の汎用版。
// 具象型（*reconnect.Transport）に依存せず、任意のステータス取得関数を受け取る。
//
// 優先順位は SelectAvailableTransport と同一:
//  1. selectedID が StatusConnected → それを返す
//  2. 他に StatusConnected があれば → それを返す
//  3. StatusReconnecting / StatusConnecting があれば → それを返す
//  4. なければ空文字列
func SelectAvailableTransportFunc(
	selectedID transport.SubConnectionID,
	transportIDs []transport.SubConnectionID,
	getStatus StatusFunc,
) transport.SubConnectionID {
	// 選択したトランスポートが接続済みか確認
	if getStatus(selectedID) == reconnect.StatusConnected {
		return selectedID
	}

	// フォールバック: 接続済みを優先、再接続中を次点
	var reconnectingID transport.SubConnectionID
	for _, id := range transportIDs {
		switch getStatus(id) {
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
	selectedID transport.SubConnectionID,
	transports TransportMap,
) transport.SubConnectionID {
	transportIDs := make([]transport.SubConnectionID, 0, len(transports))
	for id := range transports {
		transportIDs = append(transportIDs, id)
	}
	return SelectAvailableTransportFunc(selectedID, transportIDs, func(id transport.SubConnectionID) reconnect.Status {
		if tr, exists := transports[id]; exists {
			return tr.Status()
		}
		return reconnect.StatusDisconnected
	})
}
