package multi

import (
	"github.com/aptpod/iscp-go/log"
	"github.com/aptpod/iscp-go/transport"
	"github.com/aptpod/iscp-go/transport/reconnect"
)

// TransportSelector は、データサイズに基づいて最適なSubConnectionIDを選択するインターフェース。
// 実装は初期化時にバックグラウンド処理を開始する責任を持つ。
type TransportSelector interface {
	// Get は指定されたデータサイズに基づいて最適なSubConnectionIDを返す。
	// bsSize: 送信するデータのバイト数
	Get(bsSize int64) transport.SubConnectionID
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
	// 選択したトランスポートが接続済みか確認
	if tr, exists := transports[selectedID]; exists {
		if tr.Status() == reconnect.StatusConnected {
			return selectedID
		}
	}

	// フォールバック: 接続済みを優先、再接続中を次点
	var reconnectingID transport.SubConnectionID
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
