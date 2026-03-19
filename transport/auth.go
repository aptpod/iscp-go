package transport

// Token はトランスポート認証で使用するトークンです。
type Token struct {
	// Token はトークン文字列です。
	Token string

	// Header はトークンを設定するHTTPヘッダー名です。
	// 空の場合、デフォルトで "Authorization" が使用されます。
	Header string
}

// TokenSource はトークンを提供するインターフェースです。
type TokenSource interface {
	Token() (*Token, error)
}

// StaticTokenSource は固定のトークンを返す TokenSource の実装です。
type StaticTokenSource struct {
	StaticToken *Token
}

// Token は TokenSource を実装します。
func (ts *StaticTokenSource) Token() (*Token, error) {
	return ts.StaticToken, nil
}
