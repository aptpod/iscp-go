// Package metrics は、トランスポートメトリクスの収集インターフェースと実装を提供します。
//
// # MetricsProvider
//
// MetricsProvider は、RTT、RTTVAR、輻輳ウィンドウ、送信中バイト数などの
// トランスポートメトリクスを取得するためのインターフェースです。
// これらのメトリクスは、ECF（Earliest Completion First）のような高度なスケジューラが
// 最適なトランスポート選択を行うために使用されます。
//
// MetricsProvider は読み取り専用のインターフェースであり、ライフサイクル管理メソッド
// （Start/Stop）を含みません。これにより、メトリクスを利用する側は
// ライフサイクル管理の詳細を意識する必要がありません。
//
// # ManagedMetricsProvider とライフサイクル管理
//
// ManagedMetricsProvider は、MetricsProvider と LifeCycler を組み合わせた
// インターフェースで、Transport実装内部でのみ使用されます。
//
// ライフサイクル管理の責務：
//   - Transport実装（例: websocket.Transport）は内部で ManagedMetricsProvider を保持
//   - Transport作成時に Start() を呼び出してバックグラウンド処理を開始
//   - Transport破棄時に Stop() を呼び出してリソースを解放
//   - 外部には MetricsProvider インターフェースとして公開（読み取り専用）
//
// この設計により、レイヤー間の責務が明確になります：
//   - websocket.Transport: ライフサイクルを完全に管理
//   - reconnect.Transport: 読み取り専用のMetricsProviderを使用し、下層のライフサイクルに干渉しない
//   - multi.PathInfo等: メトリクスの読み取りのみを行う
//
// # 実装
//
// TCPInfoProvider (Linux のみ):
//   - TCP_INFO syscall を介してカーネルから直接メトリクスを取得
//   - 正確な Smoothed RTT、RTTVAR、輻輳ウィンドウを提供
//   - バックグラウンドで定期的にメトリクスを更新（デフォルト: 100ms）
//   - BytesInFlight をカーネルレベルで管理（Unacked * MSS）
//
// NoopMetricsProvider (全プラットフォーム):
//   - Null Object Pattern の実装
//   - メトリクスが利用できない環境でデフォルト値を返す
//   - Start/Stop は何もしない（安全な空実装）
//
// QUICMetricsProvider (全プラットフォーム):
//   - quic-go v0.59 の qlog イベントストリーム (qlogwriter.Trace / qlog.MetricsUpdated) を介して
//     トランスポートメトリクスを取得
//   - push 型: quic-go が RecordEvent で MetricsUpdated イベントを送信するたびに更新
//   - RTT / RTTVariance / CongestionWindow / BytesInFlight を抽出
//   - コネクション終了時は Recorder.Close() で内部値を 0 クリア
//   - TCP 実装と同じデフォルト値挙動 (値==0 ならデフォルトを返却)
//
// # 将来の拡張
//
// MetricsProvider インターフェースは、複数の実装をサポートするように設計されています:
//   - Ping/Pong ベースのメトリクス（クロスプラットフォーム）
//   - WebTransport API メトリクス
//   - プラットフォーム固有の syscall（例: macOS の SO_STAT）
package metrics
