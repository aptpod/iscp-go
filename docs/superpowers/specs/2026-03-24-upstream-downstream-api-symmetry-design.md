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
  │    Writer.Close(ctx) error                      │
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
type DownstreamReader struct {
    ctx        context.Context
    cancel     context.CancelFunc
    ch         chan *DownstreamDataPoint
    filterIdx  int
    downstream *Downstream
}

// UpstreamWriter は、特定のDataIDに対するデータポイント書き込みを行うWriterです。
type UpstreamWriter struct {
    dataID   *message.DataID
    upstream *Upstream
}
```

## Downstream Reader 詳細

### 公開API

```go
// NewReader は、指定フィルタインデックスに合致するDataPointを読み取るReaderを作成します。
func (d *Downstream) NewReader(ctx context.Context, filterIndex int) (*DownstreamReader, error)

// Read は、次のDataPointを1件読み取ります。データがない場合はブロックします。
func (r *DownstreamReader) Read(ctx context.Context) (*DownstreamDataPoint, error)

// Close は、Readerを閉じてdemuxerへの登録を解除します。
func (r *DownstreamReader) Close() error
```

### フィルタリング方式

サーバーが各 `DownstreamChunk` に付与する `DownstreamFilterReferences` を利用する。

- `DownstreamFilterReferences` の外側スライスは Chunk 内の各 DataPointGroup に対応
- 内側の `DownstreamFilterReference` は `DownstreamFilterIndex`（OpenDownstream 時の filters スライスのインデックス）を持つ
- Reader は自身の `filterIdx` と比較するだけ（O(1) 振り分け）

### 内部 Demuxer

```
DownstreamChunk (from server)
       │
       ▼
  ┌─────────┐
  │ demuxer │──── filterIdx=0 ──→ Reader A の ch
  │         │──── filterIdx=1 ──→ Reader B の ch
  │         │──── (unmatched) ──→ ReadChunk の ch (既存)
  └─────────┘
```

**動作:**
1. Downstream 内部の `readDataPointsLoop` が Chunk を受信
2. demuxer が `DownstreamFilterReferences` を見て各 DataPointGroup を振り分け
3. 登録済み Reader の filterIndex にマッチ → その Reader の ch へ `DownstreamDataPoint` を送信
4. どの Reader にもマッチしない or Reader が未登録 → 既存の `ReadChunk` 用チャネルへ Chunk ごと送信
5. ACK の処理は demuxer レイヤーで一元管理

**Reader 登録・解除:**
- `NewReader` 呼び出し時に `Downstream.readers` マップ（`map[int][]*DownstreamReader`）に登録
- `Reader.Close()` で登録解除しチャネルをクローズ
- ミューテックスで保護

## Upstream Writer 詳細

### 公開API

```go
// NewWriter は、指定DataIDへのWriterを作成します。
func (u *Upstream) NewWriter(dataID *message.DataID) *UpstreamWriter

// Write は、データポイントを内部バッファに書き込みます。
func (w *UpstreamWriter) Write(ctx context.Context, dps ...*message.DataPoint) error

// Close は、Writerを閉じます。
func (w *UpstreamWriter) Close(ctx context.Context) error
```

### 内部動作

- Writer は Upstream の共有 `sendBuffer` に書き込む（既存の `WriteDataPoints` と同じパス）
- `Upstream.Flush()` で全 Writer のデータがまとめて 1 Chunk として送信
- Writer は DataID を束縛した薄いラッパー

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

**シーケンス番号を内部管理とする理由:**
- ACK 追跡（`sentStorage`）やリトライ（resume 時の再送）がシーケンス番号の連続性に依存
- ユーザーが管理すると番号の重複・欠落で ACK メカニズムが壊れるリスク
- 既存の `sequenceNumberGenerator` による一元管理がプロトコルの整合性を保証

## エラーハンドリング

### Downstream Reader

| ケース | 動作 |
|---|---|
| Downstream が Close される | Reader の `Read()` が `errors.ErrStreamClosed` を返す |
| Reader の ctx がキャンセル | `Read()` が `ctx.Err()` を返す |
| 無効な filterIndex を指定 | `NewReader` がエラーを返す（OpenDownstream 時の filters 数でバリデーション） |
| 同一 filterIndex で複数 Reader | 許容する。同じデータが両方の Reader に送信される（fan-out） |
| ReadChunk と Reader を併用 | Reader にマッチしたデータは ReadChunk に流れない。マッチしないデータのみ ReadChunk で取得可能 |

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
| WriteChunk と Flush が同時実行 | ミューテックスで排他制御。シーケンス番号の一貫性を保証 |

## テスト戦略

既存テストパターン（テーブル駆動 + `t.Parallel()` + `goleak`）に従う。

### Downstream Reader テスト

| テストケース | 内容 |
|---|---|
| `TestDownstreamReader_Read` | filterIndex=0 の Reader で DataPoint を1件ずつ正しく読めること（QoS: Reliable/Unreliable） |
| `TestDownstreamReader_MultipleReaders` | 異なる filterIndex の Reader が正しくデータを振り分けられること |
| `TestDownstreamReader_SameFilterFanOut` | 同一 filterIndex で複数 Reader 作成時に両方にデータが届くこと |
| `TestDownstreamReader_WithReadChunk` | Reader と ReadChunk の併用。Reader にマッチしないデータが ReadChunk で取得できること |
| `TestDownstreamReader_Close` | Reader Close 後に demuxer 登録が解除されること |
| `TestDownstreamReader_StreamClosed` | Downstream Close 時に `ErrStreamClosed` が返ること |
| `TestDownstreamReader_InvalidFilterIndex` | 範囲外の filterIndex で `NewReader` がエラーを返すこと |

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
| `iscp/data.go` | `DownstreamDataPoint`, `UpstreamWriter`, `DownstreamReader` 型追加 |
| `iscp/upstream.go` | `WriteChunk`, `NewWriter` メソッド追加。`WriteDataPoints` に deprecated コメント |
| `iscp/downstream.go` | `ReadChunk`, `NewReader` メソッド追加。demuxer ロジック追加。`ReadDataPoints` に deprecated コメントと `ReadChunk` 委譲 |
| `iscp/upstream_test.go` | Writer, WriteChunk テスト追加 |
| `iscp/downstream_test.go` | Reader, ReadChunk テスト追加 |
| `iscp/export_test.go` | 必要に応じてテスト用ヘルパー追加 |
