package multi

import "time"

// ECF (Earliest Completion First) スケジューリングアルゴリズムで使用される定数

const (
	// ecfBeta は ECF アルゴリズムのβ定数です。
	// 第2不等式で使用され、待機判定の閾値を決定します。
	// 値が大きいほど、待機しやすくなります。
	ecfBeta = 4

	// mss は Maximum Segment Size（最大セグメントサイズ）です。
	// 一般的なTCPセグメントの最大ペイロードサイズ（バイト単位）を表します。
	// イーサネットのMTU 1500バイトから、IP/TCPヘッダ（20+20=40バイト）を引いた値です。
	//nolint:unused // export_test.go でテスト用にエクスポート
	mss = 1460

	// defaultCWND はデフォルトの輻輳ウィンドウサイズ（バイト単位）です。
	// TCP接続の初期輻輳ウィンドウとして RFC 6928 で推奨される10セグメント分です。
	// メトリクスが取得できない場合のフォールバック値として使用されます。
	//nolint:unused // export_test.go でテスト用にエクスポート
	defaultCWND = 10 * mss // 14,600 bytes
)

// abs は time.Duration の絶対値を返します。
//
//nolint:unused // export_test.go でテスト用にエクスポート
func abs(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// rttToMicroseconds は time.Duration を マイクロ秒単位の uint64 に変換します。
// ECFアルゴリズムの計算で使用されます。
func rttToMicroseconds(d time.Duration) uint64 {
	return uint64(d.Microseconds())
}

// microsecondsToRTT は マイクロ秒単位の uint64 を time.Duration に変換します。
//
//nolint:unused // export_test.go でテスト用にエクスポート
func microsecondsToRTT(us uint64) time.Duration {
	return time.Duration(us) * time.Microsecond
}
