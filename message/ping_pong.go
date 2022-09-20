package message

type (
	// Pingは、ノードとブローカーの間で疎通確認のために交換されるメッセージです。
	Ping struct {
		RequestID                            // リクエストID
		ExtensionFields *PingExtensionFields // 拡張フィールド
	}
	// PingExtensionFieldsは、Pingに含まれる拡張フィールドです。
	PingExtensionFields struct{}

	// Pongは、Pingに対する応答です。
	Pong struct {
		RequestID                            // リクエストID
		ExtensionFields *PongExtensionFields // 拡張フィールド
	}
	// PongExtensionFieldsは、Pongに含まれる拡張フィールドです。
	PongExtensionFields struct{}
)

func (_ *Ping) isMessage() {}

func (_ *Pong) isMessage() {}
