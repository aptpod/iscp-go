# Upstream/Downstream API 対称性 実装計画

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Upstream と Downstream の公開APIに対称性を持たせ、双方に Chunk レベルと DataPoint レベルの両APIを提供する。

**Architecture:** Upstream に薄いラッパー（UpstreamWriter）と即時送信API（WriteChunk）を追加。Downstream に demuxer レイヤーを追加し、DownstreamFilterReferences ベースで DataPoint 単位に振り分ける Reader パターンを実装する。既存 API は deprecated として残し後方互換性を維持。

**Tech Stack:** Go 1.23, iSCP-go (`iscp/` パッケージ), `sync/atomic`, `golang.org/x/sync/errgroup`

**Spec:** `docs/superpowers/specs/2026-03-24-upstream-downstream-api-symmetry-design.md`

---

## ファイル構成

| ファイル | 役割 |
|---|---|
| `iscp/data.go` | `DownstreamDataPoint` 型追加 |
| `iscp/upstream_writer.go` | **新規** `UpstreamWriter` 型・`NewWriter`・`Write`・`Close` |
| `iscp/upstream.go` | `WriteChunk` 追加、`validateState` リファクタリング、`WriteDataPoints` deprecated |
| `iscp/downstream_reader.go` | **新規** `DownstreamReader` 型・`Read`・`Close` |
| `iscp/downstream.go` | `ReadChunk`・`NewReader` 追加、demuxer ロジック、チャネル型変更、`ReadDataPoints` deprecated |
| `iscp/upstream_test.go` | Writer・WriteChunk テスト追加 |
| `iscp/downstream_test.go` | Reader・ReadChunk テスト追加 |
| `iscp/export_test.go` | テスト用ヘルパー追加 |

---

## Phase 1: Upstream（シンプル側から着手）

### Task 1: UpstreamWriter 型とテスト

**Files:**
- Create: `iscp/upstream_writer.go`
- Modify: `iscp/upstream.go` (NewWriter メソッド追加)
- Modify: `iscp/upstream_test.go` (テスト追加)

- [ ] **Step 1: UpstreamWriter 型を定義**

`iscp/upstream_writer.go` を作成:

```go
package iscp

import (
	"context"
	"sync/atomic"

	"github.com/aptpod/iscp-go/errors"
	"github.com/aptpod/iscp-go/message"
)

// UpstreamWriter は、特定のDataIDに対するデータポイント書き込みを行うWriterです。
type UpstreamWriter struct {
	dataID   *message.DataID
	upstream *Upstream
	closed   atomic.Bool
}

// Write は、データポイントを内部バッファに書き込みます。
func (w *UpstreamWriter) Write(ctx context.Context, dps ...*message.DataPoint) error {
	if w.closed.Load() {
		return errors.New("writer is closed")
	}
	return w.upstream.WriteDataPoints(ctx, w.dataID, dps...)
}

// Close は、Writerを閉じます。
// Close はブロックしない。バッファ内のデータは次の Flush で送信される。
func (w *UpstreamWriter) Close() error {
	if w.closed.Swap(true) {
		return errors.New("writer already closed")
	}
	return nil
}
```

- [ ] **Step 2: Upstream に NewWriter メソッドを追加**

`iscp/upstream.go` に追加:

```go
// NewWriter は、指定DataIDへのWriterを作成します。
func (u *Upstream) NewWriter(dataID *message.DataID) *UpstreamWriter {
	return &UpstreamWriter{
		dataID:   dataID,
		upstream: u,
	}
}
```

- [ ] **Step 3: Writer テストを書く**

`iscp/upstream_test.go` に追加。既存の `TestUpstream_SendDataPointWithAck` パターンを参考に:
- `TestUpstreamWriter_Write`: Writer 経由で書き込み → Flush → サーバーで受信確認
- `TestUpstreamWriter_MultipleWriters`: 異なる DataID の Writer → 同一 Chunk に含まれること
- `TestUpstreamWriter_Close`: Close 後の Write がエラーを返すこと

- [ ] **Step 4: テスト実行**

Run: `go test -v -race -count=1 ./iscp/ -run TestUpstreamWriter -timeout 30s`
Expected: PASS

- [ ] **Step 5: コミット**

```bash
git add iscp/upstream_writer.go iscp/upstream.go iscp/upstream_test.go
git commit -m "feat(iscp): add UpstreamWriter for DataPoint-level writing"
```

---

### Task 2: validateState リファクタリング

**Files:**
- Modify: `iscp/upstream.go:393-404` (validateState シグネチャ変更)
- Modify: `iscp/upstream.go:440` (flush 内の呼び出し変更)

- [ ] **Step 1: 既存テストが通ることを確認**

Run: `go test -v -race -count=1 ./iscp/ -run TestUpstream -timeout 60s`
Expected: PASS（変更前のベースライン）

- [ ] **Step 2: validateState をパラメータ化**

`iscp/upstream.go` の `validateState` を変更:

```go
// 変更前
func (u *Upstream) validateState() error {
	before := atomic.LoadUint64(&u.totalDataPoints)
	newVal := before + uint64(u.sendBufferDataPointsCount)
	if before > newVal {
		return fmt.Errorf("total datapoints exceeded max value")
	}
	if u.sequence.CurrentValue() == math.MaxUint32 {
		return fmt.Errorf("sequence number exceeded max")
	}
	return nil
}

// 変更後
func (u *Upstream) validateState(dataPointCount int) error {
	before := atomic.LoadUint64(&u.totalDataPoints)
	newVal := before + uint64(dataPointCount)
	if before > newVal {
		return fmt.Errorf("total datapoints exceeded max value")
	}
	if u.sequence.CurrentValue() == math.MaxUint32 {
		return fmt.Errorf("sequence number exceeded max")
	}
	return nil
}
```

- [ ] **Step 3: flush 内の呼び出しを更新**

`iscp/upstream.go` の `flush` メソッド内:

```go
// 変更前
if err := u.validateState(); err != nil {

// 変更後
if err := u.validateState(u.sendBufferDataPointsCount); err != nil {
```

- [ ] **Step 4: 既存テストが引き続き通ることを確認**

Run: `go test -v -race -count=1 ./iscp/ -run TestUpstream -timeout 60s`
Expected: PASS（リファクタリングのみ、動作変更なし）

- [ ] **Step 5: コミット**

```bash
git add iscp/upstream.go
git commit -m "refactor(iscp): parameterize validateState for WriteChunk support"
```

---

### Task 3: Upstream WriteChunk 実装とテスト

**Files:**
- Modify: `iscp/upstream.go` (WriteChunk メソッド、toUpstreamChunkDirect ヘルパー追加)
- Modify: `iscp/upstream_test.go` (WriteChunk テスト追加)
- Modify: `iscp/export_test.go` (必要に応じてヘルパー追加)

- [ ] **Step 1: WriteChunk テストを書く**

`iscp/upstream_test.go` に追加:
- `TestUpstream_WriteChunk`: 複数 DataPointGroup を即座に送信（QoS: Reliable/Unreliable）
- `TestUpstream_WriteChunkWithAck`: ACK が正しく処理されること
- `TestUpstream_WriteChunkSequenceShared`: WriteChunk と Flush のシーケンス番号が連続すること
- `TestUpstream_WriteChunkEmpty`: 空 groups で何も送信しないこと
- `TestUpstream_WriteChunkStreamClosed`: Close 済みで `ErrStreamClosed`
- `TestUpstream_WriteChunkTotalDataPoints`: totalDataPoints が正しくインクリメント
- `TestUpstream_WriteChunkHook`: SendDataPointsHooker が呼ばれること

- [ ] **Step 2: テスト実行（失敗確認）**

Run: `go test -v -race -count=1 ./iscp/ -run TestUpstream_WriteChunk -timeout 30s`
Expected: FAIL（WriteChunk 未実装）

- [ ] **Step 3: toUpstreamChunkDirect ヘルパーを実装**

`iscp/upstream.go` に追加。`DataPointGroup` スライスから `message.UpstreamChunk` を構成する。
既存の `toUpstreamChunk` を参考に、`sendBuffer` ではなく引数の groups を使う点が異なる。

```go
func (u *Upstream) toUpstreamChunkDirect(groups []*DataPointGroup) (*message.UpstreamChunk, *UpstreamChunk) {
	dpgs := DataPointGroups(groups)
	dpg, ids := dpgs.toUpstreamDataPointGroups(u.revDataIDAliases)
	chunk := &message.UpstreamChunk{
		StreamIDAlias: u.idAlias,
		DataIDs:       ids,
		StreamChunk: &message.StreamChunk{
			SequenceNumber:  u.sequence.Next(),
			DataPointGroups: dpg,
		},
	}
	return chunk, &UpstreamChunk{
		SequenceNumber:  chunk.StreamChunk.SequenceNumber,
		DataPointGroups: dpgs,
	}
}
```

- [ ] **Step 4: WriteChunk メソッドを実装**

`iscp/upstream.go` に追加:

```go
// WriteChunk は、複数のDataPointGroupを1つのChunkとして即座に送信します。
// シーケンス番号は内部で自動的に割り当てられます。
func (u *Upstream) WriteChunk(ctx context.Context, groups ...*DataPointGroup) error {
	if len(groups) == 0 {
		return nil
	}
	if u.isClosed() {
		return errors.ErrStreamClosed
	}
	if u.state.Is(streamStatusDraining) {
		return errors.New("draining")
	}

	// groups 内の DataPoint 数を算出
	var dataPointCount int
	for _, g := range groups {
		dataPointCount += len(g.DataPoints)
	}

	u.mu.Lock()

	if err := u.validateState(dataPointCount); err != nil {
		// 注意: closeWithError は stateWithoutLock() を呼ぶため u.mu を保持したまま呼ぶ必要がある
		// （既存の flush メソッドと同じパターン）
		u.closeWithError(u.ctx, err)
		u.mu.Unlock()
		return err
	}

	atomic.AddUint64(&u.totalDataPoints, uint64(dataPointCount))
	msgChunk, chunk := u.toUpstreamChunkDirect(groups)

	if u.sendDataPointsHooker != nil {
		u.eventDispatcher.addHandler(func() {
			u.sendDataPointsHooker.HookBefore(u.ID, *chunk)
		})
	}

	if err := u.sent.Store(u.ctx, u.ID, msgChunk.StreamChunk.SequenceNumber, chunk.DataPointGroups); err != nil {
		u.mu.Unlock()
		return err
	}

	resultCh := make(chan *message.UpstreamChunkResult)
	u.upstreamChunkResultChs[msgChunk.StreamChunk.SequenceNumber] = resultCh
	u.mu.Unlock()

	go u.sendChunkAndWaitAck(ctx, msgChunk, resultCh)
	return nil
}
```

- [ ] **Step 5: テスト実行**

Run: `go test -v -race -count=1 ./iscp/ -run TestUpstream_WriteChunk -timeout 60s`
Expected: PASS

- [ ] **Step 6: 全 Upstream テストが通ることを確認**

Run: `go test -v -race -count=1 ./iscp/ -run TestUpstream -timeout 120s`
Expected: PASS

- [ ] **Step 7: コミット**

```bash
git add iscp/upstream.go iscp/upstream_test.go iscp/export_test.go
git commit -m "feat(iscp): add WriteChunk for immediate chunk-level sending"
```

---

### Task 4: Upstream WriteDataPoints deprecated

**Files:**
- Modify: `iscp/upstream.go` (deprecated コメント追加)

- [ ] **Step 1: WriteDataPoints に deprecated コメント追加**

```go
// Deprecated: NewWriter と Writer.Write を使用してください。
// WriteDataPointsは、データポイントを内部バッファに書き込みます。
func (u *Upstream) WriteDataPoints(ctx context.Context, dataID *message.DataID, dps ...*message.DataPoint) error {
```

- [ ] **Step 2: lint 確認**

Run: `make lint`
Expected: PASS

- [ ] **Step 3: コミット**

```bash
git add iscp/upstream.go
git commit -m "refactor(iscp): deprecate WriteDataPoints in favor of NewWriter"
```

---

## Phase 2: Downstream（demuxer アーキテクチャ変更）

### Task 5: DownstreamDataPoint 型と ReadChunk リネーム

**Files:**
- Modify: `iscp/data.go` (DownstreamDataPoint 型追加)
- Modify: `iscp/downstream.go` (ReadChunk 追加、ReadDataPoints を deprecated + 委譲)
- Modify: `iscp/downstream_test.go` (ReadChunk テスト追加)

- [ ] **Step 1: DownstreamDataPoint 型を data.go に追加**

```go
// DownstreamDataPoint は、DataPoint単位でのダウンストリームデータです。
type DownstreamDataPoint struct {
	// データID
	DataID *message.DataID
	// データポイント
	DataPoint *message.DataPoint
	// アップストリーム情報
	UpstreamInfo *message.UpstreamInfo
}
```

- [ ] **Step 2: ReadChunk メソッドを追加（ReadDataPoints の内容をそのまま移動）**

`iscp/downstream.go`:

```go
// ReadChunk は、ダウンストリームチャンクを受信します。
func (d *Downstream) ReadChunk(ctx context.Context) (*DownstreamChunk, error) {
	// 既存の ReadDataPoints の実装をそのまま移動
	select {
	case <-d.ctx.Done():
		return nil, errors.ErrStreamClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	case dps := <-d.dataPointsCh:
		d.processUpstreamAlias(dps.UpstreamOrAlias)
		d.processDataPoints(dps.StreamChunk.DataPointGroups)
		ps, err := d.wireToDownstreamChunk(dps)
		if err != nil {
			d.logger.Errorf(d.ctx, "protocol error: %+v", err)
			return nil, err
		}
		d.pushResultAckBuffer(&message.DownstreamChunkResult{
			ResultCode:               message.ResultCodeSucceeded,
			ResultString:             "OK",
			SequenceNumberInUpstream: dps.StreamChunk.SequenceNumber,
			StreamIDOfUpstream:       ps.UpstreamInfo.StreamID,
		})
		return ps, nil
	}
}

// Deprecated: ReadChunk を使用してください。
// ReadDataPointsは、ダウンストリームデータポイントを受信します。
func (d *Downstream) ReadDataPoints(ctx context.Context) (*DownstreamChunk, error) {
	return d.ReadChunk(ctx)
}
```

- [ ] **Step 3: ReadChunk テストを書く**

`iscp/downstream_test.go` に追加:
- `TestDownstream_ReadChunk`: 既存の `TestDownstream_ReadDataPoint` と同じ動作であること
- `TestDownstream_ReadDataPointsDeprecated`: deprecated API が ReadChunk に委譲すること

- [ ] **Step 4: テスト実行**

Run: `go test -v -race -count=1 ./iscp/ -run "TestDownstream_ReadChunk|TestDownstream_ReadDataPointsDeprecated" -timeout 30s`
Expected: PASS

- [ ] **Step 5: 全 Downstream テストが通ることを確認**

Run: `go test -v -race -count=1 ./iscp/ -run TestDownstream -timeout 120s`
Expected: PASS

- [ ] **Step 6: コミット**

```bash
git add iscp/data.go iscp/downstream.go iscp/downstream_test.go
git commit -m "feat(iscp): add DownstreamDataPoint type and ReadChunk method"
```

---

### Task 6: Downstream demuxer インフラ（チャネル型変更と readDataPointsLoop リファクタ）

**Files:**
- Modify: `iscp/downstream.go` (チャネル型変更、readDataPointsLoop に demuxer ロジック追加、Reader 登録マップ追加)
- Modify: `iscp/conn.go` (OpenDownstream 内のチャネル初期化変更)

これは最も複雑なタスク。既存の ReadChunk テストが通り続けることを保証しながら進める。

- [ ] **Step 1: 既存テストが通ることを確認（ベースライン）**

Run: `go test -v -race -count=1 ./iscp/ -run TestDownstream -timeout 120s`
Expected: PASS

- [ ] **Step 2: Downstream 構造体にReader管理フィールドを追加**

`iscp/downstream.go` の `Downstream` 構造体に追加:

```go
// Reader 管理
readers   map[uint32][]*DownstreamReader
readersMu sync.RWMutex

// demuxer 用の処理済みチャネル（Reader が1つ以上ある時に使用）
processedDataPointsCh chan *DownstreamChunk
```

- [ ] **Step 3: readDataPointsLoop を demuxer 対応に完全に書き換え**

既存の `readDataPointsLoop` の本体を**完全に置き換え**する。旧コード（`select { case d.dataPointsCh <- dps: default: }` ブロック）は全て削除し、以下の新しい実装に差し替える。エイリアス処理・型変換・ACK push を `readDataPointsLoop` 内に移動し、Reader がある場合はフィルタ振り分け、ない場合は `processedDataPointsCh` へ送信する。

```go
func (d *Downstream) readDataPointsLoop(ctx context.Context) {
	for dps := range d.dataPointOrDone(ctx) {
		// 1. エイリアス処理（ReadChunk から移動）
		d.processUpstreamAlias(dps.UpstreamOrAlias)
		d.processDataPoints(dps.StreamChunk.DataPointGroups)

		// 2. ワイヤ形式から公開型へ変換
		chunk, err := d.wireToDownstreamChunk(dps)
		if err != nil {
			d.logger.Errorf(d.ctx, "protocol error: %+v", err)
			continue
		}

		// 3. ACK push（Chunk 単位）
		d.pushResultAckBuffer(&message.DownstreamChunkResult{
			ResultCode:               message.ResultCodeSucceeded,
			ResultString:             "OK",
			SequenceNumberInUpstream: dps.StreamChunk.SequenceNumber,
			StreamIDOfUpstream:       chunk.UpstreamInfo.StreamID,
		})

		// 4. demuxer: Reader への振り分け
		d.demux(chunk)
	}
}
```

- [ ] **Step 4: demux メソッドを実装**

```go
func (d *Downstream) demux(chunk *DownstreamChunk) {
	d.readersMu.RLock()
	hasReaders := len(d.readers) > 0
	d.readersMu.RUnlock()

	if !hasReaders {
		// Reader がいない → 既存パス
		select {
		case d.processedDataPointsCh <- chunk:
		default:
		}
		return
	}

	// Reader がいる → DataPointGroup ごとに振り分け
	var unmatchedGroups DataPointGroups
	var unmatchedFilterRefs [][]*message.DownstreamFilterReference

	for i, dpg := range chunk.DataPointGroups {
		matched := false

		// FilterReferences を確認
		if i < len(chunk.DownstreamFilterReferences) {
			for _, ref := range chunk.DownstreamFilterReferences[i] {
				d.readersMu.RLock()
				readers, ok := d.readers[ref.DownstreamFilterIndex]
				d.readersMu.RUnlock()
				if ok && len(readers) > 0 {
					matched = true
					dp := &DownstreamDataPoint{
						DataID:       dpg.DataID,
						UpstreamInfo: chunk.UpstreamInfo,
					}
					for _, dataPoint := range dpg.DataPoints {
						for _, reader := range readers {
							point := &DownstreamDataPoint{
								DataID:       dp.DataID,
								DataPoint:    dataPoint,
								UpstreamInfo: dp.UpstreamInfo,
							}
							select {
							case reader.ch <- point:
							default:
								d.logger.Warnf(d.ctx, "reader channel full, dropping data point for filterIdx=%d", ref.DownstreamFilterIndex)
							}
						}
					}
				}
			}
		}

		if !matched {
			unmatchedGroups = append(unmatchedGroups, dpg)
			if i < len(chunk.DownstreamFilterReferences) {
				unmatchedFilterRefs = append(unmatchedFilterRefs, chunk.DownstreamFilterReferences[i])
			}
		}
	}

	// unmatched があれば ReadChunk 用チャネルへ
	if len(unmatchedGroups) > 0 {
		partialChunk := &DownstreamChunk{
			SequenceNumber:             chunk.SequenceNumber,
			DataPointGroups:            unmatchedGroups,
			UpstreamInfo:               chunk.UpstreamInfo,
			DownstreamFilterReferences: unmatchedFilterRefs,
		}
		select {
		case d.processedDataPointsCh <- partialChunk:
		default:
		}
	}
}
```

- [ ] **Step 5: ReadChunk を processedDataPointsCh から読むように変更**

```go
func (d *Downstream) ReadChunk(ctx context.Context) (*DownstreamChunk, error) {
	select {
	case <-d.ctx.Done():
		return nil, errors.ErrStreamClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	case chunk := <-d.processedDataPointsCh:
		return chunk, nil
	}
}
```

- [ ] **Step 6: Downstream 構造体と OpenDownstream のチャネル初期化を更新**

**6a. `iscp/downstream.go` の `Downstream` 構造体から `dataPointsCh` フィールドを削除し `processedDataPointsCh` に置き換え:**

```go
// 変更前
dataPointsCh   chan *message.DownstreamChunk

// 変更後（削除して以下に置き換え）
processedDataPointsCh chan *DownstreamChunk
```

`dataPointsCh` は `readDataPointsLoop` 内でワイヤ型からの変換処理を行うようになったため不要。`dpsCh`（ワイヤからの生チャネル）はそのまま残す。

**6b. `iscp/conn.go` の `OpenDownstream` 内（L489付近）のチャネル初期化を変更:**

```go
// 変更前
dataPointsCh:                make(chan *message.DownstreamChunk, 1024),

// 変更後
processedDataPointsCh:       make(chan *DownstreamChunk, 1024),
```

バッファサイズは既存と同じ 1024 を維持。

**6c. Resume パスの確認:** `Downstream.resume()` は `d.dpsCh = dpsCh` のみ変更し `dataPointsCh` には触れていない。`processedDataPointsCh` も同様に resume 時に再初期化しない（既存動作と同等）。`readDataPointsLoop` が新しい `dpsCh` から読み取りを再開すれば、処理済みデータが `processedDataPointsCh` に流れる。

- [ ] **Step 7: 既存テストが引き続き通ることを確認**

Run: `go test -v -race -count=1 ./iscp/ -run TestDownstream -timeout 120s`
Expected: PASS（動作は既存と同じ、内部構造のみ変更）

- [ ] **Step 8: コミット**

```bash
git add iscp/downstream.go iscp/conn.go
git commit -m "refactor(iscp): move alias/ACK processing into readDataPointsLoop demuxer"
```

---

### Task 7: DownstreamReader 型と NewReader/Read/Close

**Files:**
- Create: `iscp/downstream_reader.go`
- Modify: `iscp/downstream.go` (NewReader メソッド追加)
- Modify: `iscp/downstream_test.go` (Reader テスト追加)

- [ ] **Step 1: Reader テストを書く**

`iscp/downstream_test.go` に追加:
- `TestDownstreamReader_Read`: filterIndex=0 の Reader で DataPoint を正しく読めること（QoS: Reliable/Unreliable）
- `TestDownstreamReader_InvalidFilterIndex`: 範囲外の filterIndex でエラー

- [ ] **Step 2: テスト実行（失敗確認）**

Run: `go test -v -race -count=1 ./iscp/ -run TestDownstreamReader -timeout 30s`
Expected: FAIL（NewReader 未実装）

- [ ] **Step 3: DownstreamReader 型を定義**

`iscp/downstream_reader.go` を作成:

```go
package iscp

import (
	"context"
	"sync/atomic"

	"github.com/aptpod/iscp-go/errors"
)

const defaultReaderChBufferSize = 256

// DownstreamReader は、フィルタ条件に合致するDataPointを1件ずつ読み取るReaderです。
type DownstreamReader struct {
	ctx        context.Context
	cancel     context.CancelFunc
	ch         chan *DownstreamDataPoint
	filterIdx  uint32
	downstream *Downstream
	closed     atomic.Bool
}

// Read は、次のDataPointを1件読み取ります。データがない場合はブロックします。
func (r *DownstreamReader) Read(ctx context.Context) (*DownstreamDataPoint, error) {
	if r.closed.Load() {
		return nil, errors.New("reader is closed")
	}
	select {
	case <-r.downstream.ctx.Done():
		return nil, errors.ErrStreamClosed
	case <-r.ctx.Done():
		return nil, r.ctx.Err()
	case <-ctx.Done():
		return nil, ctx.Err()
	case dp, ok := <-r.ch:
		if !ok {
			return nil, errors.ErrStreamClosed
		}
		return dp, nil
	}
}

// Close は、Readerを閉じてdemuxerへの登録を解除します。
func (r *DownstreamReader) Close() error {
	if r.closed.Swap(true) {
		return errors.New("reader already closed")
	}
	r.cancel()
	r.downstream.unregisterReader(r)
	return nil
}
```

- [ ] **Step 4: Downstream に NewReader と Reader 登録/解除メソッドを追加**

`iscp/downstream.go` に追加:

```go
// NewReader は、指定フィルタインデックスに合致するDataPointを読み取るReaderを作成します。
func (d *Downstream) NewReader(ctx context.Context, filterIndex uint32) (*DownstreamReader, error) {
	if int(filterIndex) >= len(d.Config.Filters) {
		return nil, fmt.Errorf("invalid filterIndex %d: must be < %d", filterIndex, len(d.Config.Filters))
	}

	readerCtx, cancel := context.WithCancel(ctx)
	reader := &DownstreamReader{
		ctx:        readerCtx,
		cancel:     cancel,
		ch:         make(chan *DownstreamDataPoint, defaultReaderChBufferSize),
		filterIdx:  filterIndex,
		downstream: d,
	}

	d.readersMu.Lock()
	if d.readers == nil {
		d.readers = make(map[uint32][]*DownstreamReader)
	}
	d.readers[filterIndex] = append(d.readers[filterIndex], reader)
	d.readersMu.Unlock()

	return reader, nil
}

func (d *Downstream) unregisterReader(r *DownstreamReader) {
	d.readersMu.Lock()
	defer d.readersMu.Unlock()

	readers := d.readers[r.filterIdx]
	for i, reader := range readers {
		if reader == r {
			d.readers[r.filterIdx] = append(readers[:i], readers[i+1:]...)
			break
		}
	}
	if len(d.readers[r.filterIdx]) == 0 {
		delete(d.readers, r.filterIdx)
	}
}
```

- [ ] **Step 5: テスト実行**

Run: `go test -v -race -count=1 ./iscp/ -run TestDownstreamReader -timeout 60s`
Expected: PASS

- [ ] **Step 6: コミット**

```bash
git add iscp/downstream_reader.go iscp/downstream.go iscp/downstream_test.go
git commit -m "feat(iscp): add DownstreamReader for DataPoint-level reading"
```

---

### Task 8: Reader 高度なテスト（振り分け、fan-out、バックプレッシャー）

**Files:**
- Modify: `iscp/downstream_test.go` (追加テスト)

- [ ] **Step 1: 振り分けテストを書く**

- `TestDownstreamReader_MultipleReaders`: 異なる filterIndex の Reader が正しく振り分けること
- `TestDownstreamReader_MultiFilterMatch`: 1つの DPG が複数 filterIndex にマッチする場合
- `TestDownstreamReader_SameFilterFanOut`: 同一 filterIndex の複数 Reader に fan-out

- [ ] **Step 2: ReadChunk 併用テストを書く**

- `TestDownstreamReader_WithReadChunk`: Reader にマッチしない DPG のみ ReadChunk で取得

- [ ] **Step 3: エッジケーステストを書く**

- `TestDownstreamReader_Close`: Close 後の Read がエラー
- `TestDownstreamReader_StreamClosed`: Downstream Close 時に `ErrStreamClosed`
- `TestDownstreamReader_Backpressure`: チャネル満杯時のドロップ確認
- `TestDownstreamReader_ACKTiming`: demuxer 処理時点で ACK 送信確認

- [ ] **Step 4: テスト実行**

Run: `go test -v -race -count=1 ./iscp/ -run TestDownstreamReader -timeout 120s`
Expected: PASS

- [ ] **Step 5: コミット**

```bash
git add iscp/downstream_test.go
git commit -m "test(iscp): add comprehensive DownstreamReader tests"
```

---

### Task 9: Downstream.Close() Reader クリーンアップ

**Files:**
- Modify: `iscp/downstream.go` (closeWithError に Reader クリーンアップ追加)

- [ ] **Step 1: closeWithError に Reader クリーンアップを追加**

`iscp/downstream.go` の `closeWithError` 内。`defer d.cancel()` によるコンテキストキャンセルで `readDataPointsLoop` が終了した**後**に Reader チャネルをクローズする必要がある。demuxer ループが停止していれば、チャネルへの書き込みは発生しないため、close-on-channel パニックは起きない。

クリーンアップを `closeWithError` の **末尾**（`return nil` の直前）に配置する:

```go
// Reader クリーンアップ（demuxer ループ終了後に安全に実行）
d.readersMu.Lock()
for _, readers := range d.readers {
	for _, reader := range readers {
		reader.closed.Store(true)
		close(reader.ch)
	}
}
d.readers = nil
d.readersMu.Unlock()
```

**注意:** `d.cancel()` が `defer` で先に呼ばれ、`readDataPointsLoop` のコンテキストがキャンセルされる。`readDataPointsLoop` は `dataPointOrDone(ctx)` のループを抜けて終了する。その後にチャネルをクローズするため、demuxer が閉じたチャネルに書き込むリスクはない。ただし、`run()` 内の `errgroup` が `readDataPointsLoop` の終了を待つタイミングとの整合性を確認すること。

- [ ] **Step 2: テスト実行（goleak 含む）**

Run: `go test -v -race -count=1 ./iscp/ -run TestDownstream -timeout 120s`
Expected: PASS（goleak でリーク検出なし）

- [ ] **Step 3: コミット**

```bash
git add iscp/downstream.go
git commit -m "fix(iscp): clean up Readers on Downstream.Close()"
```

---

### Task 10: ReadDataPoints deprecated と最終確認

**Files:**
- Modify: `iscp/downstream.go` (deprecated コメント確認)

- [ ] **Step 1: ReadDataPoints の deprecated コメントを確認**

Task 5 で既に追加済み。コメントが正しいことを確認。

- [ ] **Step 2: 全テスト実行**

Run: `go test -v -race -count=1 ./iscp/ -timeout 300s`
Expected: PASS

- [ ] **Step 3: lint 実行**

Run: `make lint`
Expected: PASS

- [ ] **Step 4: コミット（必要な場合のみ）**

```bash
git add iscp/
git commit -m "refactor(iscp): finalize API symmetry deprecations and lint fixes"
```

---

## 実行順序と依存関係

```
Task 1 (UpstreamWriter) ──→ Task 4 (WriteDataPoints deprecated)
        │
Task 2 (validateState) ──→ Task 3 (WriteChunk)
                                    │
Task 5 (ReadChunk rename) ──→ Task 6 (demuxer) ──→ Task 7 (Reader) ──→ Task 8 (Reader tests)
                                                           │
                                                    Task 9 (Close cleanup) ──→ Task 10 (最終確認)
```

**並行可能:**
- Task 1 と Task 2 は独立して実行可能
- Task 5 は Task 1-4 と並行可能（Upstream/Downstream は独立）
