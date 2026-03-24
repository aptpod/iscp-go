# Upstream/Downstream API 対称性設計

## 概要

iSCP-go の Upstream と Downstream の公開APIに対称性を持たせる。現状 Upstream は DataPoint レベル（高レベル）のAPIのみ、Downstream は Chunk レベル（低レベル）のAPIのみを提供しているが、双方に両レベルのAPIを追加する。

## 背景と動機

### 現在の非対称性

- **Upstream**: `WriteDataPoints(ctx, dataID, ...DataPoint)` → DataID単位でバッファに書き込み、内部で自動的にChunkに組み立てて送信
- **Downstream**: `ReadDataPoints(ctx)` → `*DownstreamChunk` をまるごと受信（中に複数の `DataPointGroup` が含まれる）

ユーザーは Upstream では Chunk を意識しなくてよいが、Downstream では常に Chunk 単位で扱う必要がある。

### 目標

- Upstream にも Chunk レベルの送信APIを追加
- Downstream にも DataPoint レベルの読み取りAPIを追加
- 命名規則の対称性を確保（`WriteChunk` ↔ `ReadChunk`, `Writer` ↔ `Reader`）

## API設計

### 全体像

```
Upstream:
  ┌─ Chunk レベル ──────────────────────────────────┐
  │  WriteChunk(ctx, ...DataPointGroup) error       │
  ├─ DataPoint レベル ──────────────────────────────┤
  │  NewWriter(dataID) *UpstreamWriter              │
  │    Writer.Write(ctx, ...DataPoint) error        │
  │    Writer.Close() error                         │
  │  Flush(ctx) error                               │
  ├─ Deprecated ────────────────────────────────────┤
  │  WriteDataPoints(ctx, dataID, ...DataPoint)     │
  └─────────────────────────────────────────────────┘

Downstream:
  ┌─ Chunk レベル ──────────────────────────────────┐
  │  ReadChunk(ctx) (*DownstreamChunk, error)       │
  ├─ DataPoint レベル ──────────────────────────────┤
  │  NewReader(ctx, filterIndex) *DownstreamReader  │
  │    Reader.Read(ctx) (*DownstreamDataPoint, error)│
  │    Reader.Close() error                         │
  ├─ Deprecated ────────────────────────────────────┤
  │  ReadDataPoints(ctx) (*DownstreamChunk, error)  │
  └─────────────────────────────────────────────────┘
```

### 新しい型

```go
// DownstreamDataPoint は、DataPoint単位でのダウンストリームデータです。
type DownstreamDataPoint struct {
    DataID       *message.DataID
    DataPoint    *message.DataPoint
    UpstreamInfo *message.UpstreamInfo
}

// DownstreamReader は、フィルタ条件に合致するDataPointを1件ずつ読み取るReaderです。
// 新規ファイル: iscp/downstream_reader.go に配置
type DownstreamReader struct {
    ctx        context.Context
    cancel     context.CancelFunc
    ch         chan *DownstreamDataPoint  // バッファサイズ: 256
    filterIdx  uint32                     // DownstreamFilterReference.DownstreamFilterIndex と同じ型
    downstream *Downstream
    closed     atomic.Bool
}

// UpstreamWriter は、特定のDataIDに対するデータポイント書き込みを行うWriterです。
// 新規ファイル: iscp/upstream_writer.go に配置
type UpstreamWriter struct {
    dataID   *message.DataID
    upstream *Upstream
    closed   atomic.Bool
}
```

## Downstream Reader 詳細

### 公開API

```go
// NewReader は、指定フィルタインデックスに合致するDataPointを読み取るReaderを作成します。
// filterIndex は OpenDownstream 時に渡した filters スライスのインデックスです。
func (d *Downstream) NewReader(ctx context.Context, filterIndex uint32) (*DownstreamReader, error)

// Read は、次のDataPointを1件読み取ります。データがない場合はブロックします。
func (r *DownstreamReader) Read(ctx context.Context) (*DownstreamDataPoint, error)

// Close は、Readerを閉じてdemuxerへの登録を解除します。
func (r *DownstreamReader) Close() error
```

### フィルタリング方式

サーバーが各 `DownstreamChunk` に付与する `DownstreamFilterReferences` を利用する。

- `DownstreamFilterReferences` の外側スライスは Chunk 内の各 DataPointGroup に対応
- 内側の `DownstreamFilterReference` は `DownstreamFilterIndex`（`uint32`、OpenDownstream 時の filters スライスのインデックス）を持つ
- Reader は自身の `filterIdx` と比較するだけで振り分け可能
- **1つの DataPointGroup が複数の異なる filterIndex にマッチする場合**: それぞれの filterIndex に対応する全 Reader に送信する（マルチ filterIndex fan-out）

### 内部 Demuxer

demuxer は遅延初期化される。`NewReader` が初めて呼ばれた時点でアクティブ化し、Reader が0個の状態では既存の動作と完全に同じパスを通る。

```
*message.DownstreamChunk (from wire)
       │
       ▼
  ┌─────────────────────────────────────────────────────────┐
  │ demuxer (readDataPointsLoop 内)                         │
  │                                                         │
  │ 1. processUpstreamAlias(chunk.UpstreamOrAlias)          │
  │ 2. processDataPoints(chunk.StreamChunk.DataPointGroups) │
  │ 3. wireToDownstreamChunk(chunk) → *DownstreamChunk      │
  │ 4. DownstreamFilterReferences で振り分け判定             │
  │                                                         │
  │ ┌─ DataPointGroup ごとに ─────────────────────────────┐ │
  │ │ filterRef にマッチする Reader あり:                   │ │
  │ │   → DownstreamDataPoint を該当 Reader の ch へ送信   │ │
  │ │   → 1つの DPG が複数 filterIdx にマッチする場合、    │ │
  │ │     すべての該当 Reader へ送信                       │ │
  │ │   → 同一 filterIdx に複数 Reader がある場合も fan-out│ │
  │ │                                                      │ │
  │ │ マッチする Reader なし:                               │ │
  │ │   → unmatchedGroups に蓄積                           │ │
  │ └──────────────────────────────────────────────────────┘ │
  │                                                         │
  │ 5. unmatchedGroups が存在する場合:                       │
  │    → 部分的な DownstreamChunk を再構成して               │
  │      ReadChunk 用 ch (processedDataPointsCh) へ送信     │
  │    unmatchedGroups が空の場合:                           │
  │    → ReadChunk 用 ch へは何も送信しない                  │
  │                                                         │
  │ 6. pushResultAckBuffer (Chunk 単位で ACK)               │
  └─────────────────────────────────────────────────────────┘
```

### ACK 処理タイミング

ACK は **demuxer がChunkを処理した時点**で即座に push する（Reader の `Read()` 呼び出しを待たない）。

**理由:**
- 現在の `ReadDataPoints` でも、チャネルから取り出した時点で ACK を push している（ユーザーがデータを実際に使用したかは関知しない）
- demuxer での ACK push は、この既存セマンティクスと同等
- Reader の `Read()` まで ACK を遅延させると、遅い Reader が ACK を遅延させ、サーバー側のタイムアウトを引き起こすリスクがある
- Chunk レベルの参照カウントによる ACK 遅延は複雑すぎる

### エイリアス処理の移動

現在 `ReadDataPoints` 内で行っている以下の処理を `readDataPointsLoop` 内の demuxer に移動する:

1. `processUpstreamAlias(chunk.UpstreamOrAlias)` — UpstreamInfo エイリアスの割り当て
2. `processDataPoints(chunk.StreamChunk.DataPointGroups)` — DataID エイリアスの割り当て
3. `wireToDownstreamChunk(chunk)` — ワイヤ形式から公開型への変換

これにより `ReadChunk` は処理済みの `*DownstreamChunk` を受け取る。

### チャネル型の変更

```go
// 変更前（既存）
dataPointsCh chan *message.DownstreamChunk  // 生のワイヤ型

// 変更後
processedDataPointsCh chan *DownstreamChunk  // 処理済みの公開型
```

`ReadChunk` は `processedDataPointsCh` から直接読み取る。エイリアス解決・型変換は demuxer が完了済み。

### バックプレッシャーポリシー

**Reader チャネル:**
- バッファサイズ: 256（高スループットストリームでも Reader の Read() 呼び出し間にバッファリング可能）
- チャネルが満杯の場合: **ログ警告を出してドロップ**する（既存の `readDataPointsLoop` が `default` ブランチでドロップする動作と一貫）
- 理由: 1つの遅い Reader がdemuxer全体をブロックし、他の Reader や ReadChunk に影響するのを防ぐ

**ReadChunk チャネル (`processedDataPointsCh`):**
- 既存の `dataPointsCh` と同じバッファサイズ・ドロップ動作を維持

### Reader 登録・解除

- `NewReader` 呼び出し時に `Downstream.readers` マップ（`map[uint32][]*DownstreamReader`）に登録
- `Reader.Close()` で登録解除しチャネルをクローズ
- `Downstream.readersMu sync.RWMutex` で保護
- demuxer はルーティング時に `readersMu.RLock()` で参照（書き込みロックは登録・解除時のみ）

### Downstream.Close() 時の Reader クリーンアップ

`Downstream.Close()` は全登録済み Reader のチャネルをクローズする:

1. `readersMu.Lock()` を取得
2. 全 Reader の `ch` をクローズし、`closed` フラグを立てる
3. `readers` マップをクリア
4. `readersMu.Unlock()`

これにより `Read()` でブロック中の goroutine が解放され、`goleak` でのリーク検出を防ぐ。

### Resume 時の動作

Downstream が再接続（resume）した場合:
- 新しい `dpsCh` が作成される（downstream.go:530）
- demuxer は新しい `dpsCh` から読み取りを再開する
- 既存の Reader はそのまま有効（Reader のチャネルは Downstream の内部チャネルとは独立）
- Resume イベント後、Reader は新しいデータの受信を再開する

## Upstream Writer 詳細

### 公開API

```go
// NewWriter は、指定DataIDへのWriterを作成します。
func (u *Upstream) NewWriter(dataID *message.DataID) *UpstreamWriter

// Write は、データポイントを内部バッファに書き込みます。
func (w *UpstreamWriter) Write(ctx context.Context, dps ...*message.DataPoint) error

// Close は、Writerを閉じます。
// Close はブロックしない。バッファ内のデータは次の Flush で送信される。
func (w *UpstreamWriter) Close() error
```

### 内部動作

- Writer は Upstream の共有 `sendBuffer` に書き込む（既存の `WriteDataPoints` と同じパス）
- `Upstream.Flush()` で全 Writer のデータがまとめて 1 Chunk として送信
- Writer は DataID を束縛した薄いラッパー
- `Close()` は `closed` フラグを立てるだけ（ブロック不要なので ctx パラメータなし。`DownstreamReader.Close()` と対称）

## Upstream WriteChunk 詳細

### 公開API

```go
// WriteChunk は、複数のDataPointGroupを1つのChunkとして即座に送信します。
// シーケンス番号は内部で自動的に割り当てられます。
func (u *Upstream) WriteChunk(ctx context.Context, groups ...*DataPointGroup) error
```

### 内部フロー

```
WriteDataPoints/Writer ──→ sendBuffer ──→ flushLoop/Flush ──→ toUpstreamChunk ──→ send
                                                                     ↑
WriteChunk ──→ (バッファ経由せず) ──→ toUpstreamChunkDirect ─────────┘
                                         │
                                  sequenceGenerator.Next()
                                  (同一ジェネレータ共有)
```

**動作:**
1. `WriteChunk` は内部バッファ (`sendBuffer`) を経由しない
2. 渡された `DataPointGroup` をそのまま `message.UpstreamChunk` に変換
3. シーケンス番号は既存の `sequenceNumberGenerator` から払い出し（Writer/Flush と共有）
4. DataID エイリアスの解決も既存ロジック (`revDataIDAliases`) を共有
5. QoS=Reliable の場合は `sentStorage` への保存・ACK 待ちも既存と同様に動作
6. `totalDataPoints` を `atomic.AddUint64` でインクリメント（`UpstreamCloseRequest` で送信するため必須）
7. `sendDataPointsHooker.HookBefore` が設定されている場合はフック呼び出し（既存 `flush` と同様）

**シーケンス番号を内部管理とする理由:**
- ACK 追跡（`sentStorage`）やリトライ（resume 時の再送）がシーケンス番号の連続性に依存
- ユーザーが管理すると番号の重複・欠落で ACK メカニズムが壊れるリスク
- 既存の `sequenceNumberGenerator` による一元管理がプロトコルの整合性を保証

### ロック戦略

`WriteChunk` は `u.mu.Lock()` を取得して以下を実行する（既存の `flush` と同じスコープ）:

1. `validateStateWithCount(dataPointCount)` — totalDataPoints オーバーフロー、シーケンス番号上限チェック
2. `atomic.AddUint64(&u.totalDataPoints, ...)` — データポイント数加算
3. `toUpstreamChunkDirect(groups)` — Chunk 構成（`revDataIDAliases` 参照、`sequence.Next()` 呼び出し）
4. `sent.Store(...)` — sentStorage への保存
5. `upstreamChunkResultChs` への resultCh 登録

ロック解放後に `go sendChunkAndWaitAck(...)` を起動。

これにより `WriteChunk` と `flush` は `u.mu` で排他され、シーケンス番号の連続性とバッファの一貫性が保証される。

### validateState のリファクタリング

既存の `validateState()` は `u.sendBufferDataPointsCount` を直接参照するため、バッファを経由しない `WriteChunk` パスでは正しく動作しない。データポイント数をパラメータとして受け取る形にリファクタリングする:

```go
// 変更前
func (u *Upstream) validateState() error {
    before := atomic.LoadUint64(&u.totalDataPoints)
    newVal := before + uint64(u.sendBufferDataPointsCount)
    ...
}

// 変更後
func (u *Upstream) validateState(dataPointCount int) error {
    before := atomic.LoadUint64(&u.totalDataPoints)
    newVal := before + uint64(dataPointCount)
    ...
}
```

- `flush` は `validateState(u.sendBufferDataPointsCount)` を呼び出す
- `WriteChunk` は groups 内の DataPoint 数を算出し `validateState(count)` を呼び出す

## エラーハンドリング

### Downstream Reader

| ケース | 動作 |
|---|---|
| Downstream が Close される | Reader の `Read()` が `errors.ErrStreamClosed` を返す |
| Reader の ctx がキャンセル | `Read()` が `ctx.Err()` を返す |
| 無効な filterIndex を指定 | `NewReader` がエラーを返す（OpenDownstream 時の filters 数でバリデーション） |
| 同一 filterIndex で複数 Reader | 許容する。同じデータが両方の Reader に送信される（fan-out） |
| 1つの DPG が複数 filterIndex にマッチ | 該当する全 filterIndex の全 Reader に送信 |
| ReadChunk と Reader を併用 | Reader にマッチした DataPointGroup は ReadChunk に流れない。マッチしない DataPointGroup のみ部分的な DownstreamChunk として ReadChunk で取得可能 |
| Reader チャネルが満杯 | ログ警告を出してドロップ |

### Upstream Writer

| ケース | 動作 |
|---|---|
| Upstream Close 後に Write | `errors.ErrStreamClosed` を返す |
| Writer Close 後に Write | エラーを返す（二重使用防止） |
| 同一 DataID で複数 Writer | 許容する。同じバッファに書き込む |

### Upstream WriteChunk

| ケース | 動作 |
|---|---|
| Upstream が Close 済み | `errors.ErrStreamClosed` を返す |
| draining 状態で呼び出し | エラーを返す（既存 WriteDataPoints と同じ） |
| 空の groups を渡す | 何もせず nil を返す（空 Chunk は送信しない） |
| totalDataPoints が上限超過 | 既存の `validateState` と同等のチェックで Upstream を close |
| WriteChunk と Flush が同時実行 | `u.mu.Lock()` で排他制御。シーケンス番号の一貫性を保証 |

## テスト戦略

既存テストパターン（テーブル駆動 + `t.Parallel()` + `goleak`）に従う。

### Downstream Reader テスト

| テストケース | 内容 |
|---|---|
| `TestDownstreamReader_Read` | filterIndex=0 の Reader で DataPoint を1件ずつ正しく読めること（QoS: Reliable/Unreliable） |
| `TestDownstreamReader_MultipleReaders` | 異なる filterIndex の Reader が正しくデータを振り分けられること |
| `TestDownstreamReader_MultiFilterMatch` | 1つの DataPointGroup が複数 filterIndex にマッチした場合、該当する全 Reader にデータが届くこと |
| `TestDownstreamReader_SameFilterFanOut` | 同一 filterIndex で複数 Reader 作成時に両方にデータが届くこと |
| `TestDownstreamReader_WithReadChunk` | Reader と ReadChunk の併用。Reader にマッチしない DataPointGroup のみ部分 Chunk として ReadChunk で取得できること |
| `TestDownstreamReader_Close` | Reader Close 後に demuxer 登録が解除されること |
| `TestDownstreamReader_StreamClosed` | Downstream Close 時に `ErrStreamClosed` が返ること |
| `TestDownstreamReader_InvalidFilterIndex` | 範囲外の filterIndex で `NewReader` がエラーを返すこと |
| `TestDownstreamReader_Backpressure` | Reader チャネル満杯時にデータがドロップされ、他の Reader に影響しないこと |
| `TestDownstreamReader_ACKTiming` | Reader 使用時でも ACK が demuxer 処理時点で正しく送信されること |

### Upstream Writer テスト

| テストケース | 内容 |
|---|---|
| `TestUpstreamWriter_Write` | Writer 経由でデータポイントが正しくバッファに書き込まれ、Flush で送信されること |
| `TestUpstreamWriter_MultipleWriters` | 異なる DataID の Writer が同一 Chunk にまとめて送信されること |
| `TestUpstreamWriter_Close` | Writer Close 後に Write がエラーを返すこと |

### Upstream WriteChunk テスト

| テストケース | 内容 |
|---|---|
| `TestUpstream_WriteChunk` | 複数 DataPointGroup を即座に 1 Chunk として送信できること（QoS: Reliable/Unreliable） |
| `TestUpstream_WriteChunkWithAck` | WriteChunk で送信した Chunk に対して ACK が正しく処理されること |
| `TestUpstream_WriteChunkSequenceShared` | WriteChunk と Flush が同一シーケンス番号ジェネレータを共有し、番号が連続すること |
| `TestUpstream_WriteChunkEmpty` | 空の groups で呼び出した場合に何も送信しないこと |
| `TestUpstream_WriteChunkStreamClosed` | Close 済み Upstream で `ErrStreamClosed` が返ること |
| `TestUpstream_WriteChunkTotalDataPoints` | WriteChunk 呼び出し後に totalDataPoints が正しくインクリメントされること |
| `TestUpstream_WriteChunkHook` | WriteChunk 時に SendDataPointsHooker が呼び出されること |

### Downstream ReadChunk テスト

| テストケース | 内容 |
|---|---|
| `TestDownstream_ReadChunk` | 既存 `ReadDataPoints` と同じ動作であること |
| `TestDownstream_ReadDataPointsDeprecated` | deprecated の `ReadDataPoints` が `ReadChunk` に委譲すること |

### モック・ヘルパー

- 既存の `WirePipe` + mock サーバーパターンを踏襲
- `export_test.go` に Reader/Writer のテスト用ヘルパーを必要に応じて追加

## 変更対象ファイル

| ファイル | 変更内容 |
|---|---|
| `iscp/data.go` | `DownstreamDataPoint` 型追加 |
| `iscp/upstream_writer.go` | **新規** `UpstreamWriter` 型とメソッド |
| `iscp/downstream_reader.go` | **新規** `DownstreamReader` 型とメソッド |
| `iscp/upstream.go` | `WriteChunk`, `NewWriter` メソッド追加。`WriteDataPoints` に deprecated コメント |
| `iscp/downstream.go` | `ReadChunk`, `NewReader` メソッド追加。demuxer ロジック追加（`readDataPointsLoop` 変更）。`dataPointsCh` を `processedDataPointsCh` に変更。`ReadDataPoints` に deprecated コメントと `ReadChunk` 委譲 |
| `iscp/upstream_test.go` | Writer, WriteChunk テスト追加 |
| `iscp/downstream_test.go` | Reader, ReadChunk テスト追加 |
| `iscp/export_test.go` | 必要に応じてテスト用ヘルパー追加 |

## 移行ガイド

### 既存ユーザーへの影響

`ReadDataPoints` と `WriteDataPoints` は deprecated になるが、内部で新APIに委譲するため**動作は変わらない**。コンパイルも通る。

### 推奨移行パス

```go
// Before (Upstream)
upstream.WriteDataPoints(ctx, dataID, dp1, dp2)

// After (Upstream)
writer := upstream.NewWriter(dataID)
defer writer.Close()
writer.Write(ctx, dp1, dp2)

// Before (Downstream)
chunk, err := downstream.ReadDataPoints(ctx)
for _, dpg := range chunk.DataPointGroups { ... }

// After (Downstream) - Chunk レベル
chunk, err := downstream.ReadChunk(ctx)

// After (Downstream) - DataPoint レベル
reader, err := downstream.NewReader(ctx, 0)
defer reader.Close()
dp, err := reader.Read(ctx)
```
