package transport

import "context"

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

// TokenSourceWithContext は、ctx を受け取ってトークンを提供する TokenSource です。
//
// トークン取得が外部の認証サーバーへの問い合わせ等でブロックしうる場合に
// 実装すると、dial に渡した ctx のキャンセル・期限が取得処理まで効くように
// なります。
//
// 本インターフェースを実装していない TokenSource では、dial に渡した ctx の
// キャンセルは Token() の完了までは効きません。dialer は Token() を goroutine
// で包んで中断可能に見せることはしません（呼び出し元が諦めた後も取得処理が
// 残留するリーク構造になるため）。
type TokenSourceWithContext interface {
	TokenSource

	// TokenWithContext は ctx を尊重してトークンを返します。
	TokenWithContext(ctx context.Context) (*Token, error)
}

// StaticTokenSource は固定のトークンを返す TokenSource の実装です。
// TokenSourceWithContext も実装します。
type StaticTokenSource struct {
	StaticToken *Token
}

// Token は TokenSource を実装します。
func (ts *StaticTokenSource) Token() (*Token, error) {
	return ts.StaticToken, nil
}

// TokenWithContext は TokenSourceWithContext を実装します。
// 固定のトークンを返すだけで常に即座に返るため、ctx は無視します。
func (ts *StaticTokenSource) TokenWithContext(_ context.Context) (*Token, error) {
	return ts.Token()
}
