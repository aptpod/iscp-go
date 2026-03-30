# Migration Guide: iscp-go v1 → v2

本ドキュメントは iscp-go v1 から v2 への移行手順をまとめたものです。

## 目次

- [概要](#概要)
- [クイックスタート](#クイックスタート)
- [破壊的変更一覧](#破壊的変更一覧)
  - [1. モジュールパスの変更](#1-モジュールパスの変更)
  - [2. パッケージの削除・統合](#2-パッケージの削除統合)
  - [3. 型のリネーム](#3-型のリネーム)
  - [4. インターフェースの変更](#4-インターフェースの変更)
  - [5. 非推奨APIの置き換え](#5-非推奨apiの置き換え)
  - [6. プロトコルバージョンの変更](#6-プロトコルバージョンの変更)
  - [7. 依存関係の変更](#7-依存関係の変更)
- [新機能](#新機能)
- [API対応表](#api対応表)

---

## 概要

iscp-go v2 は以下の変更を含むメジャーバージョンアップです:

- **プロトコル**: iSCPv2 v3.0.0 → v4.0.0
- **アーキテクチャ**: `wire/` パッケージの `iscp/` への統合、`encoding/` パッケージの再構成
- **新機能**: DataPoint レベル API、マルチトランスポート負荷分散、トランスポートレベルハートビート
- **パフォーマンス**: 中間 goroutine 削減、`sync.Cond` の全廃、アロケーション削減

## クイックスタート

### 1. モジュールを更新

```bash
# go.mod を更新
go get github.com/aptpod/iscp-go/v2@latest

# 旧バージョンを削除
go mod tidy
```

### 2. import パスを一括置換

```bash
# プロジェクト全体の import を置換
find . -name '*.go' -exec sed -i 's|github.com/aptpod/iscp-go/|github.com/aptpod/iscp-go/v2/|g' {} +

# bare import（サブパッケージなし）がある場合
find . -name '*.go' -exec sed -i 's|"github.com/aptpod/iscp-go"|"github.com/aptpod/iscp-go/v2"|g' {} +
```

### 3. 削除されたパッケージの import を修正

```go
// v1
import "github.com/aptpod/iscp-go/wire"
import "github.com/aptpod/iscp-go/encoding"

// v2 — これらのパッケージは削除されました。代替APIを使用してください（後述）。
```

### 4. リネームされた型を更新

```go
// v1
var id transport.TransportID
var groupID transport.TransportGroupID

// v2
var id transport.SubConnectionID
var groupID transport.SuperConnectionID
```

### 5. ビルドして残りのエラーを修正

```bash
go build ./...
```

---

## 破壊的変更一覧

### 1. モジュールパスの変更

| 項目 | v1 | v2 |
|------|----|----|
| module path | `github.com/aptpod/iscp-go` | `github.com/aptpod/iscp-go/v2` |
| import 例 | `import "github.com/aptpod/iscp-go/iscp"` | `import "github.com/aptpod/iscp-go/v2/iscp"` |

### 2. パッケージの削除・統合

#### `wire/` パッケージ → `iscp/` に統合

`wire/` パッケージは完全に削除され、その機能は `iscp/` パッケージに統合されました。

| v1 (wire/) | v2 での対応 | 備考 |
|------------|------------|------|
| `wire.ClientConn` | 非公開 (`iscp.protocolSession`) | 直接使用は不要 |
| `wire.ClientConnConfig` | 非公開 | `iscp.ConnConfig` 経由で設定 |
| `wire.EncodingTransport` | `transport.MessageTransport` | 新しいメッセージレベル I/O |
| `wire.AliasGenerator` | 非公開 (`iscp.aliasGenerator`) | 内部で自動管理 |
| `wire.Pipe()` | 削除 | テスト用は `transport.Pipe()` を使用 |
| `wire.PipeWithSize()` | 削除 | テスト用は `transport.Pipe()` を使用 |
| `wire.Copy()` | 削除 | 直接コピーなし |

#### `encoding/` パッケージ → 再構成

v1 の `encoding/` パッケージは、**ルートパッケージの型** と **サブパッケージ (json, protobuf, convert)** の2層で構成されていました。v2 ではこれらが以下のように分離されています:

- **ルートの型** (`Encoding`, `Transport`, `Size`, `Count` 等) → `transport` パッケージに統合・リネーム
- **サブパッケージ** (`json/`, `protobuf/`, `convert/`) → `encoding/` 配下にそのまま維持

v2 の `encoding/` にはルートパッケージ（`package encoding`）は存在せず、サブパッケージのみが配置されています。

**ルートパッケージの型の対応:**

| v1 (`encoding.XXX`) | v2 での対応 | 備考 |
|----------------------|------------|------|
| `encoding.Encoding` interface | `transport.Encoding` interface | パッケージ移動、メソッドは同一 |
| `encoding.Transport` struct | `transport.MessageTransport` struct | 構造体名変更 |
| `encoding.NewTransport(*TransportConfig)` | `transport.NewMessageTransport(...)` | コンストラクタのシグネチャ変更 |
| `encoding.TransportConfig` struct | `transport.MessageTransportConfig` struct | 構造体名変更 |
| `encoding.Name` type | `transport.EncodingName` type | 型名変更 |
| `encoding.ContentType` type | `transport.ContentType` type | パッケージ移動 |
| `encoding.Size` type (`B`, `KB`, `MB` 等) | 削除 | `int64` (バイト単位) を直接使用 |
| `encoding.Count` struct | `transport.Count` struct | パッケージ移動 |
| `encoding/encodingmock/` | 削除 | テスト用モックは不要に |

**サブパッケージの対応:**

| v1 | v2 | 備考 |
|----|----|------|
| `encoding/json/` | `encoding/json/` | パッケージパス維持 |
| `encoding/protobuf/` | `encoding/protobuf/` | パッケージパス維持 |
| `encoding/convert/` | `encoding/convert/` | パッケージパス維持 |

**移行例:**

```go
// v1
import (
    "github.com/aptpod/iscp-go/encoding"
    "github.com/aptpod/iscp-go/encoding/json"
)
enc := json.NewEncoding()
t := encoding.NewTransport(&encoding.TransportConfig{
    Transport:      rawTransport,
    Encoding:       enc,
    MaxMessageSize: encoding.MB * 4,
})

// v2 — ルートの encoding パッケージの import は不要
import (
    "github.com/aptpod/iscp-go/v2/transport"
    "github.com/aptpod/iscp-go/v2/encoding/json"
)
enc := json.NewEncoding()
mt := transport.NewMessageTransport(&transport.MessageTransportConfig{
    Transport:      rawTransport,
    Encoding:       enc,
    MaxMessageSize: 4 * 1024 * 1024,
})
```

#### `wire/wiremock/` → 削除

テスト用モックは `wire/wiremock/` から削除されました。v2 ではより高レベルな `iscp` パッケージのテストヘルパーを使用してください。

### 3. 型のリネーム

| v1 | v2 | パッケージ |
|----|----|-----------|
| `transport.TransportID` | `transport.SubConnectionID` | `transport` |
| `transport.TransportGroupID` | `transport.SuperConnectionID` | `transport` |

この変更はマルチトランスポート関連の全 API に影響します:

```go
// v1
type NegotiationParams struct {
    TransportID      string
    TransportGroupID string
}

// v2
type NegotiationParams struct {
    SubConnectionID   string
    SuperConnectionID string
}
```

影響を受ける型・関数:
- `multi.EventSchedulerFunc` のパラメータ型
- `multi.TransportMap` の定義
- `multi.ByteBalancedSelector` の全メソッド
- `multi.RoundRobinSelector` の全メソッド
- トランスポートセレクター関数全般

### 4. インターフェースの変更

#### `transport.Encoding` (旧 `encoding.Encoding`)

```go
// v2 の Encoding インターフェース（transport パッケージに移動）
type Encoding interface {
    EncodeTo(io.Writer, message.Message) (int, error)
    DecodeFrom(io.Reader) (int, message.Message, error)
    ContentType() ContentType
    Name() EncodingName
}
```

#### セレクター関数のシグネチャ変更

```go
// v1
func (s *ByteBalancedSelector) Get(bsSize int64) transport.TransportID

// v2 — context 追加、型リネーム
func (s *ByteBalancedSelector) Get(ctx context.Context, bsSize int64) transport.SubConnectionID
```

### 5. 非推奨APIの置き換え

以下の API は v2 で非推奨 (`Deprecated`) となりました。今後のバージョンで削除される可能性があります。

| 非推奨 API | 代替 API | 備考 |
|-----------|---------|------|
| `Upstream.WriteDataPoints()` | `Upstream.NewWriter()` + `Writer.Write()` | DataPoint レベル書き込み |
| `Downstream.ReadDataPoints()` | `Downstream.ReadChunk()` or `NewReader(ctx, filterIndex)` + `Reader.Read()` | DataPoint レベル読み込み |
| `multi.ECFTransportUpdater` | `multi.TransportMetricsUpdater` | 型エイリアス名変更 |

**移行例 (Upstream):**

```go
// v1 — WriteDataPoints を直接呼び出し
err := upstream.WriteDataPoints(ctx, dataID, dp1, dp2)

// v2 推奨 — Writer を使用
writer := upstream.NewWriter(dataID)
defer writer.Close()
err := writer.Write(ctx, dp1, dp2)
```

**移行例 (Downstream):**

```go
// v1 — ReadDataPoints でチャンク単位読み込み
chunk, err := downstream.ReadDataPoints(ctx)

// v2 推奨 — Reader で DataPoint 単位読み込み
reader, err := downstream.NewReader(ctx, filterIndex)
defer reader.Close()
dp, err := reader.Read(ctx)
```

### 6. プロトコルバージョンの変更

| 項目 | v1 | v2 |
|------|----|----|
| デフォルト ProtocolVersion | `3.0.0` | `4.0.0` |
| 受け入れ可能範囲 | — | `v2.0.0` 〜 `v4.x.x` (`v5.0.0` 未満) |

v2 モジュールは **プロトコル v2, v3, v4 をすべてサポート** しており、サーバーが返すバージョンに応じて自動的に動作を分岐します:

| サーバーのプロトコルバージョン | Keepalive 方式 | ResumeToken |
|-------------------------------|---------------|-------------|
| v2.x | iSCP レベル Ping/Pong | 非サポート |
| v3.x | iSCP レベル Ping/Pong | サポート |
| v4.x | トランスポートレベル Heartbeat | サポート |

v4 で追加された機能:
- トランスポートレベルのハートビート (Ping/Pong がワイヤレベルからトランスポートレベルに移動)
- メッセージタイププレフィックス (`0x00` = iSCP, `0x01` = Heartbeat)
- WebSocket メッセージ境界フレーミング

非推奨となった結果コード (v4.0.0+):
- `ResultCodePingTimeout` (0x4F)
- `ResultCodeTooLongPingTimeout` (0x57)
- `ResultCodeTooShortPingInterval` (0x58)
- `ResultCodeTooShortPingTimeout` (0x59)

新規追加された結果コード:
- `ResultCodeIncompatibleVersion` — バージョン不一致報告用

### 7. 依存関係の変更

**削除された依存関係:**

| パッケージ | 理由 |
|-----------|------|
| `nhooyr.io/websocket` | WebSocket 実装を `coder/websocket` (デフォルト) と `gorilla/websocket` (`DialFunc` で指定) に統一 |
| `go.uber.org/mock` | テストモック生成の依存を削除 |

**WebSocket 実装の選択:**

```
v1: coder (default) / gorilla (ビルドタグ: gorilla) / nhooyr (ビルドタグ: nhooyr)
v2: coder (default) / gorilla (DialerConfig.DialFunc で指定)
```

v2 ではビルドタグによる切り替えが廃止され、`DialerConfig.DialFunc` に `GorillaDial` を設定する方式に変わりました。`nhooyr` は削除されたため、`coder` (デフォルト) または `gorilla` への移行が必要です。

```go
// v1 — ビルドタグで切り替え
// go build -tags gorilla ./...

// v2 — DialerConfig で明示的に指定
dialer := websocket.NewDialer(websocket.DialerConfig{
    DialFunc: websocket.GorillaDial,
})
```

---

## 新機能

### DataPoint レベル API

Upstream/Downstream で DataPoint 単位の読み書きが可能になりました。

```go
// UpstreamWriter — 特定の DataID に対する書き込み
writer := upstream.NewWriter(dataID)
defer writer.Close()
writer.Write(ctx, dataPoint)

// DownstreamReader — フィルタインデックスに応じた DataPoint 読み込み
reader, err := downstream.NewReader(ctx, filterIndex)
defer reader.Close()
dp, err := reader.Read(ctx) // *DownstreamDataPoint を返す
```

### マルチトランスポート負荷分散セレクター

```go
import "github.com/aptpod/iscp-go/v2/transport/multi"

// ByteBalanced — 送信バイト数に基づく均等分散
state := multi.NewByteBalancedState()
id := multi.SelectTransportByteBalanced(transportIDs, getTxBytes, state)

// ECF (Earliest Completion First) — RTT・輻輳考慮の最適選択
ecfState := multi.NewECFState()
selector := multi.NewECFSelector()

// MinRTT — 最小 RTT に基づく選択
minState := multi.NewMinRTTState()
id := multi.SelectTransportMinRTT(transports, minState)
```

### プロトコルフレーミングユーティリティ

```go
import "github.com/aptpod/iscp-go/v2/transport/protocol"

// メッセージフレーミング
framed := protocol.FrameMessage(payload)
chunks := protocol.SplitIntoChunks(data, protocol.DefaultMaxChunkSize)

// メッセージタイプ判定
msgType, err := protocol.ParseMessageType(data)
if msgType == protocol.MessageTypeHeartbeat {
    // ハートビート処理
}
```

### トランスポートメトリクス

```go
// MessageTransport でメッセージタイプ別の統計を取得
rxCount := messageTransport.RxCount() // 受信統計
txCount := messageTransport.TxCount() // 送信統計
// rxCount.ByteCount[messageType] — 受信バイト数
// txCount.MessageCount[messageType] — 送信メッセージ数
```

---

## API対応表

包括的な v1 → v2 API マッピングです。

### パッケージレベル

| v1 パッケージ | v2 パッケージ | 状態 |
|-------------|-------------|------|
| `iscp-go/wire` | `iscp-go/v2/iscp` (内部統合) | **削除** |
| `iscp-go/encoding` | `iscp-go/v2/encoding` (型は `transport` に統合) | **再構成** |
| `iscp-go/encoding/json` | `iscp-go/v2/encoding/json` | **パス維持** |
| `iscp-go/encoding/protobuf` | `iscp-go/v2/encoding/protobuf` | **パス維持** |
| `iscp-go/wire/wiremock` | (削除) | **削除** |
| `iscp-go/encoding/encodingmock` | (削除) | **削除** |
| (なし) | `iscp-go/v2/transport/protocol` | **新規** |

### 型レベル

| v1 | v2 | 備考 |
|----|----|------|
| `encoding.Encoding` | `transport.Encoding` | パッケージ移動 |
| `encoding.Transport` | `transport.MessageTransport` | 構造体名変更 |
| `encoding.Name` | `transport.EncodingName` | パッケージ移動 |
| `encoding.ContentType` | `transport.ContentType` | パッケージ移動 |
| `encoding.Size` | `int64` | 型削除、バイト単位の整数を使用 |
| `wire.ClientConn` | (非公開) | 内部統合 |
| `wire.EncodingTransport` | `transport.MessageTransport` | 統合 |
| `transport.TransportID` | `transport.SubConnectionID` | リネーム |
| `transport.TransportGroupID` | `transport.SuperConnectionID` | リネーム |
| (なし) | `iscp.UpstreamWriter` | **新規** |
| (なし) | `iscp.DownstreamReader` | **新規** |
| (なし) | `iscp.DownstreamDataPoint` | **新規** |
| (なし) | `multi.ByteBalancedState` | **新規** |
| (なし) | `multi.ECFState` | **新規** |
| (なし) | `multi.MinRTTState` | **新規** |
| (なし) | `protocol.MessageType` | **新規** |
| (なし) | `protocol.HeartbeatMessage` | **新規** |
