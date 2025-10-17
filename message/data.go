package message

import (
	"time"

	uuid "github.com/google/uuid"
)

type (
	// UpstreamChunkは、ストリームチャンク（上り用）です。
	UpstreamChunk struct {
		StreamIDAlias   uint32                        // ストリームIDエイリアス
		DataIDs         []*DataID                     // データID
		StreamChunk     *StreamChunk                  // ストリームチャンク
		ExtensionFields *UpstreamChunkExtensionFields // 拡張フィールド
	}
	// UpstreamChunkExtensionFieldsは、ストリームチャンク（上り用）に含まれる拡張フィールドです。
	UpstreamChunkExtensionFields struct{}

	// UpstreamChunkAckは、ストリームチャンク（上り用）に対する確認応答です。
	UpstreamChunkAck struct {
		StreamIDAlias   uint32                           // ストリームIDエイリアス
		Results         []*UpstreamChunkResult           // 処理結果
		DataIDAliases   map[uint32]*DataID               // データIDエイリアス
		ExtensionFields *UpstreamChunkAckExtensionFields // 拡張フィールド
	}

	// UpstreamChunkAckExtensionFieldsは、ストリームチャンク（上り用）に対する確認応答に含まれる拡張フィールドです。
	UpstreamChunkAckExtensionFields struct{}

	// DownstreamChunkは、ストリームチャンク（下り用）です。
	DownstreamChunk struct {
		StreamIDAlias              uint32                          // ストリームIDエイリアス
		UpstreamOrAlias            UpstreamOrAlias                 // アップストリーム情報、またはアップストリームエイリアス
		StreamChunk                *StreamChunk                    // ストリームチャンク
		ExtensionFields            *DownstreamChunkExtensionFields // 拡張フィールド
		DownstreamFilterReferences [][]*DownstreamFilterReference  // マッチしたデータフィルタの参照
	}
	// DownstreamChunkExtensionFieldsは、ストリームチャンク（下り用）に含まれる拡張フィールドです。
	DownstreamChunkExtensionFields struct{}

	// DownstreamChunkAckは、ストリームチャンク（下り用）に対する確認応答です。
	DownstreamChunkAck struct {
		StreamIDAlias   uint32                             // ストリームIDエイリアス
		AckID           uint32                             // ACK ID
		Results         []*DownstreamChunkResult           // 処理結果
		UpstreamAliases map[uint32]*UpstreamInfo           // アップストリームエイリアス
		DataIDAliases   map[uint32]*DataID                 // データIDエイリアス
		ExtensionFields *DownstreamChunkAckExtensionFields // 拡張フィールド
	}

	// DownstreamChunkAckExtensionFieldsは、ストリームチャンク（下り用）に対する確認応答に含まれる拡張フィールドです。
	DownstreamChunkAckExtensionFields struct{}

	// DownstreamChunkAckCompleteは、ストリームチャンク（下り用）に対する確認応答に対する応答です。
	DownstreamChunkAckComplete struct {
		StreamIDAlias   uint32                                     // ストリームIDエイリアス
		AckID           uint32                                     // ACK ID
		ResultCode      ResultCode                                 // 結果コード
		ResultString    string                                     // 結果文字列
		ExtensionFields *DownstreamChunkAckCompleteExtensionFields // 拡張フィールド
	}
	// DownstreamChunkAckCompleteExtensionFieldsは、ストリームチャンク（下り用）に対する確認応答に対する応答に含まれる拡張フィールドです。
	DownstreamChunkAckCompleteExtensionFields struct{}
)

func (_ *UpstreamChunk) isMessage() {}

func (_ *UpstreamChunkAck) isMessage() {}

func (_ *DownstreamChunk) isMessage() {}

func (_ *DownstreamChunkAck) isMessage() {}

func (_ *DownstreamChunkAckComplete) isMessage() {}

func (_ *UpstreamChunk) isStream() {}

func (_ *UpstreamChunkAck) isStream() {}

func (_ *DownstreamChunk) isStream() {}

func (_ *DownstreamChunkAck) isStream() {}

func (_ *DownstreamChunkAckComplete) isStream() {}

// DataPointは、データポイントです。
//
// データポイントは、経過時間を付与されたバイナリデータです。 バイナリデータのことをペイロードと呼びます。
type DataPoint struct {
	ElapsedTime time.Duration // 経過時間
	Payload     []byte        // ペイロード
}

// DEPRECATED: 代わりにDataPointGroupを使用して下さい。
type UpstreamDataPointGroup = DataPointGroup

// UpstreamChunkResultは、ストリームチャンク（上り用）で送信されたデータポイントの処理結果です。
type UpstreamChunkResult struct {
	SequenceNumber  uint32                              // シーケンス番号
	ResultCode      ResultCode                          // 結果コード
	ResultString    string                              // 結果文字列
	ExtensionFields *UpstreamChunkResultExtensionFields // 拡張フィールド
}

// UpstreamChunkResultExtensionFieldsは、ストリームチャンク（上り用）の処理結果に含まれる拡張フィールドです。
type UpstreamChunkResultExtensionFields struct{}

// DEPRECATED: use DataPointGroup instead
type DownstreamDataPointGroup = DataPointGroup

// DownstreamChunkResultは ストリームチャンク（下り用）で送信されたデータポイントの処理結果です。
type DownstreamChunkResult struct {
	StreamIDOfUpstream       uuid.UUID                             // アップストリームのストリームID
	SequenceNumberInUpstream uint32                                // アップストリームにおけるシーケンス番号
	ResultCode               ResultCode                            // 結果コード
	ResultString             string                                // 結果文字列
	ExtensionFields          *DownstreamChunkResultExtensionFields // 拡張フィールド
}

// DownstreamChunkResultExtensionFieldsは、ストリームチャンク（下り用）で送信されたデータポイントの処理結果に含まれる拡張フィールドです。
type DownstreamChunkResultExtensionFields struct{}

// UpstreamOrAliasは、アップストリーム情報、またはアップストリームエイリアスです。
type UpstreamOrAlias interface {
	isUpstreamOrAlias()
}

// UpstreamAliasは、アップストリームエイリアスです。
type UpstreamAlias uint32

func (_ UpstreamAlias) isUpstreamOrAlias() {}

// UpstreamInfoは、アップストリーム情報です。
type UpstreamInfo struct {
	SessionID    string
	SourceNodeID string
	StreamID     uuid.UUID
}

func (_ *UpstreamInfo) isUpstreamOrAlias() {}

// DataIDOrAliasは、データID、またはデータIDのエイリアスです。
type DataIDOrAlias interface {
	isDataIDOrAlias()
}

// DataIDAliasは、データIDのエイリアスです。
type DataIDAlias uint32

func (_ DataIDAlias) isDataIDOrAlias() {}
func (_ *DataID) isDataIDOrAlias()     {}

// StreamChunkは、ストリームを時間で区切ったデータポイントのまとまりです。
//
// iSCP におけるデータ伝送は、このチャンク単位で行われます。
type StreamChunk struct {
	SequenceNumber  uint32
	DataPointGroups []*DataPointGroup
}

// DataPointGroupは、ストリームチャンクの中のデータポイントをデータIDごとにまとめた集合です。
type DataPointGroup struct {
	DataIDOrAlias DataIDOrAlias // データID または データIDエイリアス
	DataPoints    []*DataPoint  // データポイント
}

// DownstreamFilterReferenceは、データフィルタへの参照です。
type DownstreamFilterReference struct {
	DownstreamFilterIndex uint32 // ダウンストリームフィルタのインデックス
	DataFilterIndex       uint32 // データフィルタのインデックス
}
