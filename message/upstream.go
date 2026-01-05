package message

import (
	"time"

	uuid "github.com/google/uuid"
)

type (
	// UpstreamOpenRequestは、アップストリーム開始要求です。
	//
	// アップストリーム開始要求を送信したノードは、ノードからブローカー方向のデータ送信ストリームを開始します。
	UpstreamOpenRequest struct {
		RequestID                                             // リクエストID
		SessionID         string                              // セッションID
		AckInterval       time.Duration                       // Ackの返却間隔
		ExpiryInterval    time.Duration                       // 有効期限
		DataIDs           []*DataID                           // データIDリスト
		QoS               QoS                                 // QoS
		ExtensionFields   *UpstreamOpenRequestExtensionFields // 拡張フィールド
		EnableResumeToken bool                                // Resumeトークン機能を有効化するかどうか
	}
	// UpstreamOpenRequestExtensionFieldsは、アップストリーム開始要求に含まれる拡張フィールドです。
	UpstreamOpenRequestExtensionFields struct {
		Persist bool // 永続化フラグ
	}

	// UpstreamOpenResponseは、アップストリーム開始要求の応答です。
	UpstreamOpenResponse struct {
		RequestID                                                  // リクエストID
		AssignedStreamID      uuid.UUID                            // 割り当てられたストリームID
		AssignedStreamIDAlias uint32                               // 割り当てられたストリームIDエイリアス
		ResultCode            ResultCode                           // 結果コード
		ResultString          string                               // 結果文字列
		ServerTime            time.Time                            // サーバー時刻
		DataIDAliases         map[uint32]*DataID                   // DataIDエイリアス
		ExtensionFields       *UpstreamOpenResponseExtensionFields // 拡張フィールド
		ResumeToken           string                               // Resumeトークン
	}
	// UpstreamOpenResponseExtensionFieldsは、アップストリーム開始要求の応答に含まれる拡張フィールドです。
	UpstreamOpenResponseExtensionFields struct{}

	// UpstreamResumeRequestは、アップストリーム再開要求です。
	UpstreamResumeRequest struct {
		RequestID                                             // リクエストID
		StreamID        uuid.UUID                             // ストリームID
		ExtensionFields *UpstreamResumeRequestExtensionFields // 拡張フィールド
		ResumeToken     string                                // Resumeトークン
	}

	// UpstreamResumeRequestExtensionFieldsは、アップストリーム再開要求に含まれる拡張フィールドです。
	UpstreamResumeRequestExtensionFields struct{}

	// UpstreamResumeResponseは、アップストリーム再開要求の応答です。
	UpstreamResumeResponse struct {
		RequestID                                                    // リクエストID
		AssignedStreamIDAlias uint32                                 // 割り当てられたストリームIDエイリアス
		ResultCode            ResultCode                             // 結果コード
		ResultString          string                                 // 結果文字列
		ExtensionFields       *UpstreamResumeResponseExtensionFields // 拡張フィールド
		ResumeToken           string                                 // Resumeトークン
	}

	// UpstreamResumeResponseExtensionFieldsは、アップストリーム再開要求の応答に含まれる拡張フィールドです。
	UpstreamResumeResponseExtensionFields struct{}

	// UpstreamCloseRequestは、アップストリーム切断要求です。
	UpstreamCloseRequest struct {
		RequestID                                                // リクエストID
		StreamID            uuid.UUID                            // ストリームID
		TotalDataPoints     uint64                               // 総データポイント数
		FinalSequenceNumber uint32                               // 最終シーケンス番号
		ExtensionFields     *UpstreamCloseRequestExtensionFields // 拡張フィールド
	}

	// UpstreamCloseRequestExtensionFieldsは、アップストリーム切断要求に含まれる拡張フィールドです。
	UpstreamCloseRequestExtensionFields struct {
		CloseSession bool //  セッションをクローズするかどうか
	}

	// UpstreamCloseResponseは、アップストリーム切断要求の応答です。
	UpstreamCloseResponse struct {
		RequestID                                             // リクエストID
		ResultCode      ResultCode                            // 結果コード
		ResultString    string                                // 結果文字列
		ExtensionFields *UpstreamCloseResponseExtensionFields // 拡張フィールド
	}

	// UpstreamCloseResponseExtensionFieldsは、アップストリーム切断要求に含まれる拡張フィールドです。
	UpstreamCloseResponseExtensionFields struct{}
)

func (*UpstreamOpenRequest) isMessage() {}

// Persistは、アップストリーム開始要求の拡張フィールドに含まれている永続化フラグの値を返却します。
func (r *UpstreamOpenRequest) Persist() bool {
	if r.ExtensionFields == nil {
		return false
	}
	return r.ExtensionFields.Persist
}

func (r *UpstreamOpenResponse) ServerTimeOrUnixZero() int64 {
	return orUnixZero(r.ServerTime)
}

func orUnixZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}

func (*UpstreamOpenResponse) isMessage() {}

func (*UpstreamResumeRequest) isMessage() {}

func (*UpstreamResumeResponse) isMessage() {}

func (*UpstreamCloseRequest) isMessage() {}

func (*UpstreamCloseResponse) isMessage() {}

// IsCloseSessionは、アップストリーム切断要求の拡張フィールドに含まれているセッション終了フラグの値を返却します。
func (r *UpstreamCloseRequest) IsCloseSession() bool {
	if r.ExtensionFields == nil {
		return false
	}
	return r.ExtensionFields.CloseSession
}
