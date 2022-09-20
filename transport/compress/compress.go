package compress

/*
Config は、 トランスポート層での圧縮に関する設定です。
*/
type Config struct {
	// Enableは圧縮の有効化です。
	//
	// Enableが `false` の場合、その他すべての圧縮設定が無視されます。
	Enable bool

	// Level は、 DEFLATE 圧縮の圧縮レベルです。
	// 詳細な設定値については、 compress/zlib パッケージの定数を参照してください。
	Level int

	// DisableContextTakeover は、 DEFLATE 圧縮のコンテキスト引き継ぎ（Context Takeover）の有効無効を設定します。
	DisableContextTakeover bool

	// WindowBits は、DEFLATE 圧縮のコンテキスト引き継ぎ（Context Takeover）におけるウィンドウサイズを表すビット数です。
	WindowBits int
}

func (c Config) Type() Type {
	if c.DisableContextTakeover {
		return TypePerMessage
	}

	return TypeContextTakeOver
}

// Type は、圧縮の形式を表します。
type Type string

const (
	// TypePerMessage は、メッセージごとに圧縮することを表します。
	TypePerMessage Type = "per-message"

	// TypeContextTakeOver は、DEFLATE 圧縮のコンテキスト引き継ぎ（Context Takeover）で圧縮することを表します。
	TypeContextTakeOver Type = "context-takeover"
)

// WindowSize は、DEFLATE 圧縮のコンテキスト引き継ぎ（Context Takeover）におけるウィンドウサイズを返します。 ( WindowSize = 2 ^ WindowBits )
func (c Config) WindowSize() int {
	return 1 << c.WindowBits
}
