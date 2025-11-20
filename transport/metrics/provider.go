package metrics

import "time"

// MetricsProvider は、トランスポートメトリクスを取得するためのインターフェースです。
// 読み取り専用で、ライフサイクル管理メソッドは含まれません。
//
// 実装は並行アクセスに対して安全である必要があります。
type MetricsProvider interface {
	// RTT は、平滑化ラウンドトリップタイム (SRTT) を返します。
	// まだ測定されていない場合は、デフォルト値（例: 100ms）を返します。
	RTT() time.Duration

	// RTTVar は、RTT変動 (RTTVAR、平均偏差とも呼ばれる) を返します。
	// まだ測定されていない場合は、デフォルト値（例: 50ms）を返します。
	RTTVar() time.Duration

	// CongestionWindow は、輻輳ウィンドウサイズをバイト単位で返します。
	// まだ測定されていない場合は、デフォルト値（例: 14600 バイト = 10 * MSS）を返します。
	CongestionWindow() uint64

	// BytesInFlight は、現在送信中のバイト数を返します
	// （送信済みだがまだ確認応答または完了していないバイト数）。
	// これはカーネルによって TCP_INFO を介して管理されます（Snd_nxt - Snd_una）。
	BytesInFlight() uint64
}

// LifeCycler は、バックグラウンド処理のライフサイクルを管理するインターフェースです。
type LifeCycler interface {
	// Start は、バックグラウンドでのメトリクス収集を開始します。
	// すでに開始されている場合、またはプロバイダーを開始できない場合はエラーを返します。
	// Start() の複数回呼び出しはエラーを返す必要があります（冪等ではありません）。
	Start() error

	// Stop は、バックグラウンド処理を終了し、リソースを解放します。
	// Stop() を呼び出した後も、プロバイダーへの問い合わせ
	// （RTT、RTTVar など）は安全ですが、キャッシュされた値またはデフォルト値を返します。
	// Stop() の複数回呼び出しは安全である必要があります（冪等です）。
	Stop()
}

// ManagedMetricsProvider は、ライフサイクル管理を含むMetricsProviderです。
// Transport実装内部でのみ使用され、外部にはMetricsProviderとして公開されます。
type ManagedMetricsProvider interface {
	MetricsProvider
	LifeCycler
}
