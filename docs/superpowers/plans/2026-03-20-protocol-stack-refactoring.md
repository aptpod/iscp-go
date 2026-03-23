# iscp-go プロトコルスタック大規模リファクタリング実行計画

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** iscp-goの4層プロトコルスタック（iscp→wire→encoding→transport）の複雑性を大幅に削減する。iscp公開APIの互換性を維持しつつ、内部レイヤーの統廃合・重複排除・バグ修正を実施する。

**Architecture:** 15の改善提案を6フェーズに分け、依存関係順に実施。Phase 1-2は独立タスクで並列実行可能。Phase 3-4は順序依存あり。Phase 5-6は前段に依存。各フェーズ完了時にテスト全通を確認。

**Tech Stack:** Go 1.24, Protocol Buffers (gogo/protobuf), QUIC (quic-go), WebSocket (coder/websocket), WebTransport

**Repository root:** `/home/masayuki/go/src/github.com/aptpod/iscp-go/.claude/worktrees/main-v2-refactoring-claude/`

---

## 依存関係グラフ

```
Phase 1 (独立) ──┐
  T1: wire.Size削除       │
  T2: nhooyr削除          ├─→ Phase 3 (transport統合) ─→ Phase 4 (レイヤーマージ) ─→ Phase 5 ─→ Phase 6
  T3: e2e.go dead code    │     T8: negotiation統合            T13: EncodingTransport簡素化
  T4: Closer埋め込み      │     T9: wire/negotiation移動        T14: encoding→wire統合
                          │     T10: EncodingName正規化         T15: build-tag移動
Phase 2 (独立) ──┘        T11: QUIC/WT共通基盤            T16: conninit→iscp統合
  T5: orDone[T]ヘルパー          T12: selector stats統合
  T6: 汎用ステートマシン
  T7: wire dispatch統合
```

---

## Phase 1: ゼロリスク即時修正

### Task 1: `wire.Size` デッドコード削除

**Files:**
- Delete: `wire/const.go`
- Verify: `wire.Size` はプロダクションコードから参照なし（`encoding.Size` が正規版）

- [ ] **Step 1: 参照がないことを確認**

```bash
cd /home/masayuki/go/src/github.com/aptpod/iscp-go/.claude/worktrees/main-v2-refactoring-claude
grep -r "wire\.Size\|wire\.B\b\|wire\.KB\|wire\.MB\|wire\.GB\|wire\.TB\|wire\.PB" --include="*.go" --exclude-dir=vendor | grep -v "_test.go" | grep -v "wire/const.go"
```
Expected: 出力なし（参照ゼロ）

- [ ] **Step 2: ファイル削除**

```bash
rm wire/const.go
```

- [ ] **Step 3: テスト通過確認**

```bash
go build ./wire/... && go test ./wire/...
```
Expected: PASS

- [ ] **Step 4: コミット**

```bash
git add -A wire/const.go
git commit -m "refactor(wire): remove dead Size type

wire.Size was an exact duplicate of encoding.Size with zero production references.

Confidence: high
Scope-risk: narrow"
```

---

### Task 2: nhooyr WebSocket サブパッケージ削除（バグ修正含む）

**Files:**
- Delete: `transport/websocket/nhooyr/` (全ファイル)
- Delete: `wire/enable_nhooyr.go`
- Modify: `wire/enable_coder.go` - build tag条件変更

**Context:** nhooyr は coder/websocket の旧版。`nhooyr/conn.go:51` に MessageText→MessageBinary の実バグあり。coder が正しい実装。

- [ ] **Step 1: nhooyr の外部参照確認**

```bash
grep -r "nhooyr" --include="*.go" --exclude-dir=vendor | grep -v "_test.go"
```
Expected: `wire/enable_nhooyr.go` と `transport/websocket/nhooyr/` 内のみ

- [ ] **Step 2: nhooyr パッケージ削除**

```bash
rm -rf transport/websocket/nhooyr/
rm wire/enable_nhooyr.go
```

- [ ] **Step 3: coder の build tag 条件更新**

`wire/enable_coder.go` の build tag を `!gorilla` のみに変更:

```go
//go:build !gorilla

package wire

import (
	_ "github.com/aptpod/iscp-go/transport/websocket/coder"
)
```

- [ ] **Step 4: go.mod から nhooyr 依存削除**

```bash
go mod tidy
```

- [ ] **Step 5: テスト通過確認**

```bash
go build ./... && go test ./transport/websocket/... ./wire/...
```
Expected: PASS

- [ ] **Step 6: コミット**

```bash
git add -A
git commit -m "refactor(transport/websocket): remove nhooyr sub-package

nhooyr/websocket is the predecessor of coder/websocket with a known bug
(MessageText incorrectly mapped to MessageBinary at conn.go:51).
coder/websocket is the maintained successor with the correct mapping.

Rejected: Fix nhooyr bug and keep both | maintaining two near-identical packages is wasteful
Confidence: high
Scope-risk: narrow
Directive: If nhooyr support is needed again, use coder/websocket which is API-compatible"
```

---

### Task 3: `e2e.go` デッドコード修正

**Files:**
- Modify: `iscp/e2e.go:67-88` - 重複 `case <-ctx.Done()` 除去

- [ ] **Step 1: デッドコード確認**

`iscp/e2e.go` の `ReceiveReplyCall` 内の2つ目の `case <-ctx.Done()` を確認。

- [ ] **Step 2: 重複case削除**

1つ目の `case <-ctx.Done()` のみ残し、2つ目を削除。

- [ ] **Step 3: テスト通過確認**

```bash
go build ./iscp/... && go test ./iscp/...
```

- [ ] **Step 4: コミット**

```bash
git add iscp/e2e.go
git commit -m "fix(iscp): remove unreachable duplicate ctx.Done case in ReceiveReplyCall

Confidence: high
Scope-risk: narrow"
```

---

### Task 4: `Closer` インターフェースを `Transport` に組み込み

**Files:**
- Modify: `transport/transport.go` - `Closer` を `Transport` に embed
- Modify: `transport/reconnect/transport.go` - runtime型アサーション除去
- Modify: 各 transport 実装が `CloseWithStatus` を実装していることを確認

- [ ] **Step 1: 全実装が CloseWithStatus を持つか確認**

```bash
grep -r "func.*CloseWithStatus" --include="*.go" transport/
```
Expected: websocket, quic, webtransport, reconnect, multi で実装済み

- [ ] **Step 2: Transport インターフェースに Closer を embed**

`transport/transport.go` を修正:

```go
type Transport interface {
	ReadWriter
	Closer  // CloseWithStatus を含む

	AsUnreliable() (tr UnreliableTransport, ok bool)
	NegotiationParams() NegotiationParams
	Name() Name
}
```

既存の `Close() error` は `ReadWriter` にあるため、`Closer` embed時に重複注意。
`Closer` の `Close()` と `ReadWriter` の `Close()` は同一シグネチャなので問題なし。

- [ ] **Step 3: reconnect 内の型アサーション除去**

`transport/reconnect/transport.go` で `transport.Closer` への型アサーションを直接メソッド呼び出しに変更。

- [ ] **Step 4: テスト通過確認**

```bash
go build ./transport/... && go test ./transport/...
```

- [ ] **Step 5: コミット**

```bash
git add transport/
git commit -m "refactor(transport): embed Closer interface into Transport

Eliminates runtime type assertions in reconnect package.
All concrete Transport implementations already implement CloseWithStatus.

Confidence: high
Scope-risk: moderate"
```

---

## Phase 2: ジェネリクス活用による重複排除

### Task 5: `orDone[T]` ジェネリクスヘルパー抽出

**Files:**
- Create: `iscp/internal_helpers.go` (またはiscp内の適切なファイル)
- Modify: `iscp/upstream.go` - `ackOrDone` を置き換え
- Modify: `iscp/downstream.go` - `dataPointOrDone`, `ackCompleteOrDone`, `metadataOrDone` を置き換え
- Modify: `iscp/conn.go` - `subscribeDownstreamMetadata` 内の `orDone` を置き換え

- [ ] **Step 1: テスト追加 - orDone ヘルパー**

`iscp/internal_helpers_test.go` を作成:

```go
package iscp

import (
	"context"
	"testing"
	"time"
)

func TestOrDone(t *testing.T) {
	t.Run("forwards values from input channel", func(t *testing.T) {
		ctx := context.Background()
		in := make(chan int, 3)
		in <- 1
		in <- 2
		in <- 3
		close(in)

		out := orDone(ctx, in)
		var results []int
		for v := range out {
			results = append(results, v)
		}
		if len(results) != 3 {
			t.Fatalf("expected 3 values, got %d", len(results))
		}
	})

	t.Run("stops on context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		in := make(chan int) // blocks forever

		out := orDone(ctx, in)
		cancel()

		select {
		case _, ok := <-out:
			if ok {
				t.Fatal("expected channel to be closed")
			}
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for channel close")
		}
	})
}
```

- [ ] **Step 2: テスト実行 - FAIL確認**

```bash
go test ./iscp/ -run TestOrDone -v
```
Expected: FAIL (orDone not defined)

- [ ] **Step 3: orDone[T] 実装**

```go
// iscp/internal_helpers.go
package iscp

import "context"

func orDone[T any](ctx context.Context, ch <-chan T) <-chan T {
	out := make(chan T)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case v, ok := <-ch:
				if !ok {
					return
				}
				select {
				case out <- v:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}
```

- [ ] **Step 4: テスト PASS 確認**

```bash
go test ./iscp/ -run TestOrDone -v
```

- [ ] **Step 5: 5箇所の重複コードを置き換え**

`upstream.go:ackOrDone`, `downstream.go:dataPointOrDone/ackCompleteOrDone/metadataOrDone`, `conn.go:orDone closure` を `orDone` 呼び出しに置き換え。

- [ ] **Step 6: 全テスト通過確認**

```bash
go test ./iscp/...
```

- [ ] **Step 7: コミット**

```bash
git add iscp/
git commit -m "refactor(iscp): extract generic orDone[T] helper to eliminate 5 duplications

Replaces 5 identical channel-adapter implementations with a single
generic function using Go 1.18+ type parameters.

Confidence: high
Scope-risk: narrow"
```

---

### Task 6: 汎用ステートマシン（`connStatus` / `streamState` 統合）

**Files:**
- Create: `iscp/state_machine.go` - ジェネリック `stateMachine[S]`
- Modify: `iscp/state.go` - `connStatus` を `stateMachine` ベースに
- Modify: `iscp/stream_state.go` - `streamState` を `stateMachine` ベースに

- [ ] **Step 1: 共通メソッドの抽出テスト作成**

`iscp/state_machine_test.go` で汎用ステートマシンのテスト。

- [ ] **Step 2: `stateMachine[S comparable]` 実装**

共通メソッド: `Swap`, `CompareAndSwap`, `CompareAndSwapNot`, `Is`, `Current`, `WaitUntil`。
`connStatus` 固有の `WaitUntilOrClosed`, `WithCloseStatus` は `connStatus` に残す。

- [ ] **Step 3: `connStatus` と `streamState` をリファクタ**

既存のテストが全て通ることを確認。

- [ ] **Step 4: コミット**

---

### Task 7: wire ClientConn ジェネリックディスパッチ

**Files:**
- Modify: `wire/client_conn_runtime.go` - 4つの同一ループをジェネリック関数化

- [ ] **Step 1: ジェネリック dispatch 関数作成**

```go
type streamAliasMessage interface {
	GetStreamIDAlias() uint32
}

func dispatchByAlias[T streamAliasMessage](
	ch <-chan T, mu *sync.RWMutex, registry map[uint32]chan T,
) {
	for msg := range ch {
		mu.RLock()
		target, ok := registry[msg.GetStreamIDAlias()]
		mu.RUnlock()
		if !ok {
			continue
		}
		select {
		case target <- msg:
		default:
		}
	}
}
```

- [ ] **Step 2: message 型に GetStreamIDAlias() が必要な場合は追加**

`message.UpstreamChunkAck`, `message.DownstreamChunk`, `message.DownstreamChunkAckComplete` に `GetStreamIDAlias()` メソッドを確認・追加。

- [ ] **Step 3: 4ループを置き換え**

`readUpstreamChunkAckLoop`, `readDownstreamChunkLoop`, `readDownstreamChunkUnreliableLoop`, `readDownstreamChunkAckCompleteLoop` を `dispatchByAlias` 呼び出しに変更。

- [ ] **Step 4: テスト通過確認**

```bash
go test ./wire/... ./message/...
```

- [ ] **Step 5: コミット**

---

## Phase 3: Transport 統合

### Task 8: ネゴシエーション関数の統合

**Files:**
- Modify: `transport/negotiation.go` - URL/Binary marshal メソッドを追加
- Delete: `transport/websocket/negotiation.go` (完全重複)
- Delete: `transport/webtransport/negotiation.go` (完全重複)
- Modify: `transport/quic/negotiation.go` - binary marshal を transport に移動

- [ ] **Step 1: websocket/webtransport の negotiation が完全同一であることを確認**

```bash
diff <(sed 's/package websocket/package X/' transport/websocket/negotiation.go) \
     <(sed 's/package webtransport/package X/' transport/webtransport/negotiation.go)
```
Expected: 差分なし

- [ ] **Step 2: URL marshal/unmarshal を transport パッケージに移動**

`transport/negotiation.go` に `MarshalURLValues()` と `UnmarshalURLValues()` を追加。

- [ ] **Step 3: Binary marshal/unmarshal を transport パッケージに移動**

`transport/negotiation.go` に `MarshalBinary()` と `UnmarshalBinary()` を追加（QUIC用）。

- [ ] **Step 4: サブパッケージのnegotiation.goを削除**

websocket/webtransport/quic の negotiation.go を削除し、呼び出し元を `transport.NegotiationParams` のメソッド呼び出しに変更。

- [ ] **Step 5: テスト通過確認 + 既存negotiationテスト移動**

```bash
go test ./transport/... ./transport/websocket/... ./transport/quic/... ./transport/webtransport/...
```

- [ ] **Step 6: コミット**

---

### Task 9: `wire/negotiation` パッケージを `transport` に移動（逆依存解消）

**Files:**
- Move: `wire/negotiation/params.go` → `transport/` 内に統合
- Modify: `transport/negotiation.go` - type alias をやめ、型を直接定義
- Modify: 全 import パスを更新

**これは Phase 3 で最も重要なタスク。**

- [ ] **Step 1: wire/negotiation の内容を transport に統合**

`wire/negotiation/params.go` の `Params` 型、`EncodingName` 型、定数を `transport/negotiation.go` に直接定義。

- [ ] **Step 2: `transport/negotiation.go` から `wire/negotiation` import を除去**

type alias (`type EncodingName = negotiationpkg.EncodingName`) を実型定義に変更。

- [ ] **Step 3: `wire/negotiation` パッケージの削除（または transport からの re-export に変更）**

後方互換性のため `wire/negotiation` は `transport` からの re-export（type alias）に変更:

```go
// wire/negotiation/params.go
package negotiation

import "github.com/aptpod/iscp-go/transport"

// Deprecated: Use transport package directly.
type EncodingName = transport.EncodingName
type Params = transport.NegotiationParams
```

- [ ] **Step 4: 全 import パス更新**

```bash
grep -r "wire/negotiation" --include="*.go" | grep -v vendor
```
全参照を `transport` パッケージに変更。

- [ ] **Step 5: テスト通過確認**

```bash
go build ./... && go test ./...
```

- [ ] **Step 6: コミット**

---

### Task 10: `EncodingName` 4重複定義の正規化

**Files:**
- Modify: `transport/negotiation.go` - 正規定義場所
- Modify: `encoding/main.go` - `encoding.Name` を `transport.EncodingName` の alias に
- Modify: `iscp/const.go` - `iscp.EncodingName` を `transport.EncodingName` の alias に
- Delete: `wire/negotiation` の独自定義（Task 9で完了済み）

- [ ] **Step 1: transport.EncodingName を正規定義に**

Task 9で移動済みの `transport.EncodingName` が正規定義。

- [ ] **Step 2: encoding.Name を type alias に変更**

```go
// encoding/main.go
type Name = transport.EncodingName
```

- [ ] **Step 3: iscp.EncodingName を type alias に変更**

```go
// iscp/const.go
type EncodingName = transport.EncodingName
```

iscp の公開APIとしての `EncodingNameProtobuf` 等の定数は残す（互換性維持）。

- [ ] **Step 4: 手動変換コード削除**

`iscp/conn_options.go:182` の `transport.EncodingName(c.Encoding.toEncoding().Name())` のような変換が不要に。

- [ ] **Step 5: テスト通過確認**

```bash
go build ./... && go test ./...
```

- [ ] **Step 6: コミット**

---

### Task 11: QUIC / WebTransport 共通基盤抽出

**Files:**
- Create: `transport/streamtransport/` - 共通ストリームトランスポート基盤
- Modify: `transport/quic/transport.go` - 共通基盤を使用
- Modify: `transport/webtransport/transport.go` - 共通基盤を使用

- [ ] **Step 1: SessionAdapter インターフェース定義**

```go
// transport/streamtransport/adapter.go
package streamtransport

import (
	"context"
	"io"
)

type SendStream interface {
	io.WriteCloser
}

type ReceiveStream interface {
	io.Reader
}

type SessionAdapter interface {
	OpenUniStreamSync(ctx context.Context) (SendStream, error)
	AcceptUniStream(ctx context.Context) (ReceiveStream, error)
	ReceiveDatagram(ctx context.Context) ([]byte, error)
	SendDatagram(data []byte) error
	CloseWithError(code uint64, msg string) error
}
```

- [ ] **Step 2: 共通 Transport 構造体の抽出**

QUIC/WebTransport で共通のフィールドとメソッド（Read, Write, Close, compression, counters, datagram handling）を `streamtransport.Transport` に抽出。

- [ ] **Step 3: QUIC adapter 実装**

```go
// transport/quic/adapter.go
type quicSessionAdapter struct {
	conn quic.Connection
}
// SessionAdapter インターフェースを実装
```

- [ ] **Step 4: WebTransport adapter 実装**

```go
// transport/webtransport/adapter.go
type wtSessionAdapter struct {
	session *webtransgo.Session
}
```

- [ ] **Step 5: quic/transport.go と webtransport/transport.go をリファクタ**

既存の Transport struct を `streamtransport.Transport` の薄いラッパーに。

- [ ] **Step 6: datagram.go も共通化**

`readBinarySet` 型、datagram read/write を共通基盤に。

- [ ] **Step 7: テスト通過確認**

```bash
go test ./transport/quic/... ./transport/webtransport/... ./transport/streamtransport/...
```

- [ ] **Step 8: コミット**

---

### Task 12: Selector stats ヘルパー抽出

**Files:**
- Create: `transport/multi/selector_stats.go`
- Modify: `transport/multi/ecf_selector.go`
- Modify: `transport/multi/minrtt_selector.go`
- Modify: `transport/multi/byte_balanced_selector.go`

- [ ] **Step 1: 共通 SelectorStats 構造体作成**

```go
type SelectorStats struct {
	mu              sync.Mutex
	totalSelections uint64
	switchCount     uint64
	selectionCounts map[transport.SubConnectionID]uint64
	lastSelected    transport.SubConnectionID
}
```

- [ ] **Step 2: 3つの selector から stats ロジックを共通化**

- [ ] **Step 3: テスト通過確認**

```bash
go test ./transport/multi/...
```

- [ ] **Step 4: コミット**

---

## Phase 4: レイヤーマージ

### Task 13: `EncodingTransport` を3メソッドに簡素化

**Files:**
- Modify: `wire/transport.go` - 8メソッド→3メソッド
- Modify: `wire/wiremock/transport.go` - mock更新
- Modify: `iscp/conn.go` 等 - カウンターアクセスを `encoding.Transport` から直接取得

- [ ] **Step 1: wire が実際に使う3メソッドだけに簡素化**

```go
// wire/transport.go
type EncodingTransport interface {
	Read() (message.Message, error)
	Write(message message.Message) error
	Close() error
}
```

- [ ] **Step 2: iscp 層でカウンター等を直接アクセスするよう変更**

iscp.Conn が `encoding.Transport` インスタンスを直接保持し、カウンター系メソッドをそこから取得。

- [ ] **Step 3: mock 再生成**

```bash
go generate ./wire/...
```

- [ ] **Step 4: テスト通過確認**

```bash
go test ./wire/... ./iscp/...
```

- [ ] **Step 5: コミット**

---

### Task 14: encoding パッケージを wire に統合

**Files:**
- Move: `encoding/main.go` の `Transport` struct → `wire/encoding_transport.go`
- Move: `encoding/counter.go` → `wire/encoding_counter.go`
- Keep: `encoding/` パッケージは `Encoding` インターフェースと `protobuf/json` サブパッケージのみ
- Modify: `wire/client_conn.go` - `ClientConnConfig` が `transport.Transport` + `Encoding` を直接受け取る

- [ ] **Step 1: encoding.Transport を wire パッケージに移動**

encoding パッケージの `Transport` struct（ReadWriter+Encoding のラッパー）を wire に移動。
`encoding` パッケージは純粋なコーデック（`Encoding` インターフェース、protobuf/json 実装、convert）のみに。

- [ ] **Step 2: wire.ClientConnConfig を更新**

```go
type ClientConnConfig struct {
	Transport       transport.Transport  // 生の transport
	Encoding        encoding.Encoding    // エンコーディング
	MaxMessageSize  encoding.Size
	// ... 残りは同じ
}
```

wire.Connect 内で encoding ラッパーを内部作成。

- [ ] **Step 3: import パス更新**

- [ ] **Step 4: テスト通過確認**

```bash
go build ./... && go test ./...
```

- [ ] **Step 5: コミット**

---

### Task 15: build-tag ファイルを wire から iscp に移動

**Files:**
- Move: `wire/enable_coder.go` → `iscp/enable_coder.go`
- Move: `wire/enable_gorilla.go` → `iscp/enable_gorilla.go`
- 理由: WebSocket 実装の選択は transport/consumer 層の関心事

- [ ] **Step 1: ファイル移動 + package 宣言変更**

- [ ] **Step 2: テスト通過確認**

- [ ] **Step 3: コミット**

---

### Task 16: `internal/conninit` を iscp に統合

**Files:**
- Delete: `internal/conninit/conninit.go`
- Modify: `iscp/conn_init.go` (新規) - conninit の内容を iscp に移動
- Modify: `iscp/conn_options.go` - conninit 呼び出しを直接呼び出しに

- [ ] **Step 1: conninit の関数を iscp に移動**

`ResolveDialer`, `NewMultiTransport`, `NewWireClientConn`, `resolveEncoding` を iscp パッケージ内の新ファイルに移動。

- [ ] **Step 2: import パス更新**

- [ ] **Step 3: internal/conninit パッケージ削除**

- [ ] **Step 4: テスト通過確認**

```bash
go build ./... && go test ./...
```

- [ ] **Step 5: コミット**

---

## Phase 5: encoding/convert 最適化

### Task 17: 空拡張フィールドスタブの統合

**Files:**
- Modify: `encoding/convert/wire_to_proto.go` - 27個の空スタブ→ジェネリック関数
- Modify: `encoding/convert/proto_to_wire.go` - 27個の空スタブ→ジェネリック関数

- [ ] **Step 1: ジェネリック nil-or-empty 関数作成**

```go
func nilOrEmpty[In, Out any](in *In, factory func() *Out) *Out {
	if in == nil {
		return nil
	}
	return factory()
}
```

- [ ] **Step 2: 54個の空スタブ関数を nilOrEmpty 呼び出しに置換**

データを持つ5個の関数（ConnectRequest, Intdash, UpstreamOpen, UpstreamClose, UpstreamMetadata の拡張フィールド）はそのまま残す。

- [ ] **Step 3: テスト通過確認**

```bash
go test ./encoding/...
```

- [ ] **Step 4: コミット**

---

### Task 18: map ベース enum 変換

**Files:**
- Modify: `encoding/convert/wire_to_proto.go` - ResultCode switch → map
- Modify: `encoding/convert/proto_to_wire.go` - ResultCode switch → map

- [ ] **Step 1: 双方向 map 定義**

```go
var resultCodeWireToProto = map[message.ResultCode]autogen.ResultCode{
	message.ResultCodeSucceeded:                autogen.ResultCode_SUCCEEDED,
	message.ResultCodeNormalClosure:            autogen.ResultCode_NORMAL_CLOSURE,
	// ... 全35エントリ
}

var resultCodeProtoToWire = map[autogen.ResultCode]message.ResultCode{
	// 逆方向マップ（自動生成可能）
}
```

- [ ] **Step 2: switch 文を map ルックアップに置換**

- [ ] **Step 3: テスト通過確認（既存テストで値の正確性を検証）**

```bash
go test ./encoding/convert/...
```

- [ ] **Step 4: コミット**

---

### Task 19: convert コード生成（または大幅簡素化）

**Files:**
- Create: `encoding/convert/gen/` - コード生成ツール（オプション）
- Modify: `encoding/convert/wire_to_proto.go` - 生成コードに置換
- Modify: `encoding/convert/proto_to_wire.go` - 生成コードに置換

**アプローチ:** Task 17, 18 で既に大幅に簡素化されているため、残りの機械的変換を `go generate` で生成する仕組みを構築。

- [ ] **Step 1: 変換ルールの定義ファイル作成**

message 型と proto 型のフィールドマッピングを宣言的に定義。

- [ ] **Step 2: 生成テンプレート作成**

メッセージごとの変換コードを生成するテンプレート。

- [ ] **Step 3: 生成 + 既存コードとの差分検証**

- [ ] **Step 4: `go generate` コマンド追加**

- [ ] **Step 5: テスト通過確認**

```bash
go generate ./encoding/convert/... && go test ./encoding/...
```

- [ ] **Step 6: コミット**

---

## Phase 6: message 型の進化

### Task 20: message 拡張フィールドの簡素化

**Files:**
- Modify: `message/*.go` - 空の ExtensionFields struct を削除可能なものから削除
- Modify: `encoding/convert/` - 対応する変換コード更新

**アプローチ:** 27個の空 `*ExtensionFields` struct のうち、外部から参照されていないものを削除。

- [ ] **Step 1: 各 ExtensionFields の外部参照を調査**

```bash
for t in $(grep -h "type.*ExtensionFields struct" message/*.go | awk '{print $2}'); do
    echo "=== $t ==="
    grep -r "$t" --include="*.go" --exclude-dir=vendor | grep -v "message/" | grep -v "encoding/convert/" | head -5
done
```

- [ ] **Step 2: 外部参照のない空 ExtensionFields を削除**

- [ ] **Step 3: 対応する convert コード更新**

- [ ] **Step 4: message 型で可能な場合は ExtensionFields を optional (ポインタ) → 値型に変更**

空structならゼロコストなので、ポインタ→値型にすることで nil チェック不要に。

- [ ] **Step 5: テスト通過確認**

```bash
go build ./... && go test ./...
```

- [ ] **Step 6: コミット**

---

### Task 21: message.ResultCode の値アラインメント検討

**Files:**
- Modify: `message/result_code.go` - iota → proto 互換の値に変更

**注意:** これは iscp 公開APIに影響する可能性あり。ResultCode を数値で比較しているユーザーがいる場合は破壊的変更。

- [ ] **Step 1: 外部からの数値比較パターンを調査**

```bash
grep -r "ResultCode.*[0-9]" --include="*.go" | grep -v vendor | grep -v message/result_code.go
```

- [ ] **Step 2: 問題なければ proto enum 値に合わせて定数値を変更**

```go
const (
	ResultCodeSucceeded     ResultCode = 0  // was iota+1
	ResultCodeNormalClosure ResultCode = 1
	// ...
	ResultCodeUnspecifiedError ResultCode = 64  // was iota
)
```

- [ ] **Step 3: convert の enum マッピングを単純キャストに変更**

```go
func toResultCodeProto(in message.ResultCode) (autogen.ResultCode, error) {
	return autogen.ResultCode(in), nil
}
```

- [ ] **Step 4: テスト通過確認**

```bash
go build ./... && go test ./...
```

- [ ] **Step 5: コミット**

---

## 全体検証

### Final Verification

- [ ] **Step 1: 全ビルド**

```bash
go build ./...
```

- [ ] **Step 2: 全テスト**

```bash
go test ./... -count=1
```

- [ ] **Step 3: go vet**

```bash
go vet ./...
```

- [ ] **Step 4: 行数比較**

```bash
find . -type f -name "*.go" -not -path "*/vendor/*" -not -path "*/.claude/*" -not -name "*_test.go" | xargs wc -l | tail -1
```
Before: ~15,000行 → After: 目標 ~12,000行以下

- [ ] **Step 5: 依存関係の正常性確認**

```bash
# transport → wire の逆依存がないこと
grep -r "wire/negotiation" --include="*.go" transport/ | grep -v vendor
```
Expected: 出力なし

---

## リスク管理

| Phase | リスク | 緩和策 |
|:-----:|:------|:------|
| 1 | なし | デッドコード削除のみ |
| 2 | ジェネリクス導入のコンパイルエラー | 各Step後にビルド確認 |
| 3 | negotiation移動時のimportパス漏れ | grep で全参照を確認 |
| 4 | encoding→wire統合時のインターフェース破壊 | iscp層のテストで検証 |
| 5 | convert変更時のシリアライゼーション互換性 | 既存テスト + encoding benchmark |
| 6 | ResultCode値変更時の後方互換性 | 外部参照パターンを事前調査 |
