// Package multi は、複数のトランスポートを束ねて単一のトランスポートとして扱うための機能を提供します。
//
// # 概要
//
// multi.Transport は、複数の reconnect.Transport を管理し、データの書き込み時に
// TransportSelector を使用して最適なトランスポートを選択します。
// これにより、複数のネットワークパスを活用した高可用性・高スループットの通信が可能になります。
//
// # TransportSelector インターフェース
//
// TransportSelector は、トランスポート選択ロジックを抽象化するインターフェースです:
//
//	type TransportSelector interface {
//	    Get(bsSize int64) transport.TransportID
//	}
//
// 提供されている実装:
//   - RoundRobinSelector: ラウンドロビン方式で順番にトランスポートを選択
//   - ByteBalancedSelector: 送信バイト数に基づいてトランスポートを選択（トラフィック均等化）
//   - ECFSelector: ECF (Earliest Completion First) アルゴリズムで最適なトランスポートを選択（スループット最適化）
//   - MinRTTSelector: MinRTT (Minimum RTT) アルゴリズムで最速のトランスポートを選択（レイテンシ最適化）
//
// # ECF (Earliest Completion First) アルゴリズム
//
// ECFSelector は、MPTCP (Multipath TCP) のスケジューラアルゴリズムに基づいた
// 高度なトランスポート選択を実装しています。
//
// アルゴリズムの概要:
//   - 各トランスポートの RTT、RTTVAR、輻輳ウィンドウ、送信中バイト数を考慮
//   - 「最も早く完了する」トランスポートを動的に選択
//   - 2つの不等式を評価して、待機すべきか即座に送信すべきか判定
//
// 2つの不等式:
//
//   - 第1不等式: 最速トランスポートを待つ価値があるか評価
//     β * srtt_f * (x_f + cwnd_f) < β * cwnd_f * (srtt_s + δ) + waiting * cwnd_f * (srtt_s + δ)
//
//   - 第2不等式: 待機が本当に有益か判定
//     srtt_s * x_s >= cwnd_s * (2 * srtt_f + δ)
//
// ここで:
//   - srtt_f: 最速トランスポートの Smoothed RTT
//   - srtt_s: 送信可能な最速トランスポートの Smoothed RTT
//   - cwnd_f, cwnd_s: 各トランスポートの輻輳ウィンドウ
//   - x_f, x_s: キューサイズと輻輳ウィンドウの最大値
//   - δ: RTT変動の最大値 (max(rttvar_f, rttvar_s))
//   - β: 調整係数 (デフォルト: 4)
//
// # 使用例
//
// RoundRobinSelector を使用した基本的な例:
//
//	transportMap := multi.TransportMap{
//	    "transport1": tr1,
//	    "transport2": tr2,
//	}
//
//	mt, err := multi.NewTransport(multi.TransportConfig{
//	    TransportMap:      transportMap,
//	    TransportSelector: multi.NewRoundRobinSelector(transportMap.TransportIDs()),
//	    Logger:            logger,
//	})
//
// ECFSelector を使用した例:
//
//	selector := multi.NewECFSelector()
//
//	mt, err := multi.NewTransport(multi.TransportConfig{
//	    TransportMap:      transportMap,
//	    TransportSelector: selector,
//	    Logger:            logger,
//	})
//
// ECFSelector または MinRTTSelector を使用する場合、multi.Transport は自動的にメトリクス更新ループを起動し、
// 各トランスポートの RTT、輻輳ウィンドウなどのメトリクスを定期的に収集します。
//
// # MinRTT (Minimum RTT) アルゴリズム
//
// MinRTTSelector は、ECFの複雑な待機判定を行わず、単純に「その時点でMinRTTが最小のトランスポート」を
// 即座に選択します。これにより、待機なしの低レイテンシ通信を実現します。
//
// ECFとMinRTTの使い分け:
//   - ECF: 待機が有益な場合は最速パスが利用可能になるまで待つ（スループット最適化）
//   - MinRTT: 待機なしで、利用可能なトランスポートの中からRTT最小のものを即座に選択（レイテンシ最適化）
//
// MinRTTSelector を使用した例:
//
//	selector := multi.NewMinRTTSelector()
//
//	mt, err := multi.NewTransport(multi.TransportConfig{
//	    TransportMap:      transportMap,
//	    TransportSelector: selector,
//	    Logger:            logger,
//	})
//
// # ByteBalanced（送信バイト数バランス）アルゴリズム
//
// ByteBalancedSelector は、各トランスポートの累積送信バイト数（TxBytesCounterValue）を比較し、
// 最も送信量が少ないトランスポートを優先的に選択します。
// これにより、複数トランスポート間の送信負荷を均等化します。
//
// RoundRobinSelectorとの違い:
//   - RoundRobin: 呼び出し回数ベースで均等に分散（データサイズを考慮しない）
//   - ByteBalanced: 送信バイト数ベースで均等に分散（大小異なるデータを均等化）
//
// ByteBalancedSelector を使用した例:
//
//	selector := multi.NewByteBalancedSelector(transportMap.TransportIDs())
//
//	mt, err := multi.NewTransport(multi.TransportConfig{
//	    TransportMap:      transportMap,
//	    TransportSelector: selector,
//	    Logger:            logger,
//	})
//
// # TransportMetricsUpdater インターフェース
//
// ECFSelector と MinRTTSelector は TransportMetricsUpdater インターフェースを実装しており、
// multi.Transport は型アサーションでこのインターフェースを検出し、
// メトリクス更新機能を有効化します:
//
//	type TransportMetricsUpdater interface {
//	    UpdateTransport(transportID transport.TransportID, info *TransportInfo)
//	    SetQueueSize(queueSize uint64)  // MinRTTSelector では no-op
//	    SetLogger(logger log.Logger)
//	}
//
// # 状態管理
//
// multi.Transport は、内部トランスポートの状態を監視し、
// 全体の状態を MultiOverallStatus として公開します:
//   - AllConnected: 全てのトランスポートが接続済み
//   - PartiallyConnected: 一部のトランスポートが接続済み
//   - AllReconnecting: 全てのトランスポートが再接続中
//   - Disconnected: 全てのトランスポートが切断済み
//
// # プラットフォーム要件
//
// ECFSelector のメトリクス取得は、Linux 環境でのみ完全にサポートされています。
// Linux 以外の環境では、デフォルト値が使用されます。
package multi
