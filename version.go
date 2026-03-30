package iscp

// Version は このライブラリのバージョンです。
const Version = "v2.0.0-next"

func SemVersion() string {
	return Version[1:]
}

// ProtocolVersion は ConnectRequest で宣言するデフォルトのプロトコルバージョンです。
// サーバーが返す実際のバージョン（v2.0.0〜v4.x.x）に応じて動作を自動分岐します。
const ProtocolVersion = "4.0.0"
