package iscp

// Version は このライブラリのバージョンです。
const Version = "v1.1.0-next"

func SemVersion() string {
	return Version[1:]
}

// ProtocolVersion は プロトコルのバージョンです。
// v2 モジュールは v4 トランスポート機能（メッセージフレーミング、ハートビート、マルチトランスポート）を
// 使用するため、プロトコルバージョン 4.0.0 を宣言します。
const ProtocolVersion = "4.0.0"
