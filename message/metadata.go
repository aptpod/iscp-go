package message

import (
	"time"

	uuid "github.com/google/uuid"
)

type (
	// UpstreamMetadataは、アップストリームメタデータです。
	//
	// メタデータを格納してノードからブローカーへ転送するためのメッセージです。
	UpstreamMetadata struct {
		RequestID                                        // リクエストID
		Metadata        SendableMetadata                 // メタデータ
		ExtensionFields *UpstreamMetadataExtensionFields // 拡張フィールド
	}

	// UpstreamMetadataExtensionFieldsは、アップストリームメタデータに含まれる拡張フィールドです。
	UpstreamMetadataExtensionFields struct {
		Persist bool // 永続化フラグ
	}

	// UpstreamMetadataAckは、アップストリームメタデータに対する応答です。
	UpstreamMetadataAck struct {
		RequestID                                           // リクエストID
		ResultCode      ResultCode                          // 結果コード
		ResultString    string                              // 結果文字列
		ExtensionFields *UpstreamMetadataAckExtensionFields // 拡張フィールド
	}

	// UpstreamMetadataAckExtensionFieldsは、アップストリームメタデータに対する応答に含まれる拡張フィールドです。
	UpstreamMetadataAckExtensionFields struct{}

	// DownstreamMetadataは、ダウンストリームメタデータです。
	//
	// メタデータを格納してブローカーからノードへ転送するためのメッセージです。
	DownstreamMetadata struct {
		RequestID       RequestID                          // リクエストID
		StreamIDAlias   uint32                             // ストリームIDエイリアス
		SourceNodeID    string                             // 生成元ノードID
		Metadata        Metadata                           // メタデータ
		ExtensionFields *DownstreamMetadataExtensionFields // 拡張フィールド
	}
	// DownstreamMetadataExtensionFieldsは、ダウンストリームメタデータに含まれる拡張フィールドです。
	DownstreamMetadataExtensionFields struct{}

	// DownstreamMetadataAckは、ダウンストリームメタデータに対する応答です。
	DownstreamMetadataAck struct {
		RequestID       RequestID                             // リクエストID
		ResultCode      ResultCode                            // 結果コード
		ResultString    string                                // 結果文字列
		ExtensionFields *DownstreamMetadataAckExtensionFields // 拡張フィールド
	}

	// DownstreamMetadataAckExtensionFieldsは、ダウンストリームメタデータに対する応答に含まれる拡張フィールドです。
	DownstreamMetadataAckExtensionFields struct{}
)

// Persistは、アップストリームメタデータの拡張フィールドに含まれている永続化フラグの値を返却します。
func (m *UpstreamMetadata) Persist() bool {
	if m.ExtensionFields == nil {
		return false
	}
	return m.ExtensionFields.Persist
}

func (_ *UpstreamMetadata) isMessage() {}

func (_ *UpstreamMetadataAck) isMessage() {}

func (_ *DownstreamMetadata) isMessage() {}

func (_ *DownstreamMetadataAck) isMessage() {}

// Metadataはメタデータです。
//
// メタデータは、iSCPによって転送されるデータに関するメタデータです。
type Metadata interface {
	// BaseTime *BaseTime // 基準時刻
	// UpstreamOpen            *UpstreamOpen            // アップストリーム開始
	// UpstreamResume          *UpstreamResume          // アップストリーム再開
	// UpstreamAbnormalClose *UpstreamAbnormalClose // アップストリーム異常終了
	// UpstreamNormalClose   *UpstreamNormalClose   // アップストリーム正常終了
	// DownstreamOpen            *DownstreamOpen            // ダウンストリーム開始
	// DownstreamResume          *DownstreamResume          // ダウンストリーム再開
	// DownstreamAbnormalClose *DownstreamAbnormalClose // ダウンストリーム異常終了
	// DownstreamNormalClose   *DownstreamNormalClose   // ダウンストリーム正常終了
	isMetadata()
}

// SendableMetadataは、アップストリームメタデータとして送信可能なメタデータです。
type SendableMetadata interface {
	Metadata
	isSendableMetadata()
}

// BaseTimeは、基準時刻です。
//
// あるセッションの基準となる時刻です。
type BaseTime struct {
	SessionID   string        // セッションID
	Name        string        // 基準時刻の名称
	Priority    uint32        // 優先度
	ElapsedTime time.Duration // 経過時間
	BaseTime    time.Time     // 基準時刻
}

// DownstreamAbnormalCloseは、あるダウンストリームが異常切断したことを知らせるメタデータです。
type DownstreamAbnormalClose struct {
	StreamID uuid.UUID // ストリームID
}

// DownstreamNormalCloseは、あるダウンストリームが正常切断したことを知らせるメタデータです。
type DownstreamNormalClose struct {
	StreamID uuid.UUID // ストリームID
}

// DownstreamOpenは、あるダウンストリームが開いたことを知らせるメタデータです。
type DownstreamOpen struct {
	StreamID          uuid.UUID           // ストリームID
	DownstreamFilters []*DownstreamFilter // ダウンストリームフィルタ
	QoS               QoS                 // QoS
}

// DownstreamResumeは、あるダウンストリームが再開したことを知らせるメタデータです。
type DownstreamResume struct {
	StreamID          uuid.UUID           // ストリームID
	DownstreamFilters []*DownstreamFilter // ダウンストリームフィルタ
	QoS               QoS                 // QoS
}

// UpstreamAbnormalCloseは、あるアップストリームが異常切断したことを知らせるメタデータです。
type UpstreamAbnormalClose struct {
	StreamID  uuid.UUID // ストリームID
	SessionID string    // セッションID
}

// UpstreamNormalCloseは、あるアップストリームが正常切断したことを知らせるメタデータです。
type UpstreamNormalClose struct {
	StreamID            uuid.UUID // ストリームID
	SessionID           string    // セッションID
	TotalDataPoints     uint64    // 総データポイント数
	FinalSequenceNumber uint32    // 最終シーケンス番号
}

// UpstreamOpenは、あるアップストリームが開いたことを知らせるメタデータです。
type UpstreamOpen struct {
	StreamID  uuid.UUID // ストリームID
	SessionID string    // セッションID
	QoS       QoS       // QoS
}

// UpstreamResumeは、あるアップストリームが再開したことを知らせるメタデータです。
type UpstreamResume struct {
	StreamID  uuid.UUID // ストリームID
	SessionID string    // セッションID
	QoS       QoS       // QoS
}

func (_ *BaseTime) isMetadata() {}

func (_ *BaseTime) isSendableMetadata() {}

func (_ *DownstreamAbnormalClose) isMetadata() {}

func (_ *DownstreamNormalClose) isMetadata() {}

func (_ *DownstreamOpen) isMetadata() {}

func (_ *DownstreamResume) isMetadata() {}

func (_ *UpstreamAbnormalClose) isMetadata() {}

func (_ *UpstreamNormalClose) isMetadata() {}

func (_ *UpstreamOpen) isMetadata() {}

func (_ *UpstreamResume) isMetadata() {}
