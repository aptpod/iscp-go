package multi

import "github.com/aptpod/iscp-go/transport"

// TransportSelector は、データサイズに基づいて最適なTransportIDを選択するインターフェース。
// 実装は初期化時にバックグラウンド処理を開始する責任を持つ。
type TransportSelector interface {
	// Get は指定されたデータサイズに基づいて最適なTransportIDを返す。
	// bsSize: 送信するデータのバイト数
	Get(bsSize int64) transport.TransportID
}
