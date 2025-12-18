package message

import (
	"time"

	uuid "github.com/google/uuid"
)

type (
	// DownstreamOpenRequestは、ダウンストリーム開始要求です。
	//
	// ダウンストリーム開始要求を受信したブローカーは、ブローカーからノード方向のデータ送信ストリームを開始します。
	DownstreamOpenRequest struct {
		RequestID                                                  // リクエストID
		DesiredStreamIDAlias uint32                                // 割り当てを希望するストリームIDエイリアス
		DownstreamFilters    []*DownstreamFilter                   // ダウンストリームフィルタ
		ExpiryInterval       time.Duration                         // 有効期限
		DataIDAliases        map[uint32]*DataID                    // データIDエイリアス
		QoS                  QoS                                   // QoS
		ExtensionFields      *DownstreamOpenRequestExtensionFields // 拡張フィールド
		OmitEmptyChunk       bool                                  // 空チャンク省略フラグ
		EnableResumeToken    bool                                  // Resumeトークン機能を有効化するかどうか
	}
	// DownstreamOpenRequestExtensionFieldsは、ダウンストリーム開始要求に含まれる拡張フィールドです。
	DownstreamOpenRequestExtensionFields struct{}

	// DownstreamOpenResponseは、 ダウンストリーム開始要求に対する応答です。
	DownstreamOpenResponse struct {
		RequestID                                               // リクエストID
		AssignedStreamID uuid.UUID                              // 割り当てられたストリームID
		ResultCode       ResultCode                             // 結果コード
		ResultString     string                                 // 結果文字列
		ServerTime       time.Time                              // サーバー時刻
		ExtensionFields  *DownstreamOpenResponseExtensionFields // 拡張フィールド
		ResumeToken      string                                 // Resumeトークン
	}
	// DownstreamOpenResponseExtensionFieldsは、ダウンストリーム開始要求に対する応答に含まれる拡張フィールドです。
	DownstreamOpenResponseExtensionFields struct{}

	// DownstreamResumeRequestは、ダウンストリーム再開要求です。
	DownstreamResumeRequest struct {
		RequestID                                                    // リクエストID
		StreamID             uuid.UUID                               // ストリームID
		DesiredStreamIDAlias uint32                                  // 割り当てを希望するストリームIDエイリアス
		ExtensionFields      *DownstreamResumeRequestExtensionFields // 拡張フィールド
		ResumeToken          string                                  // Resumeトークン
	}

	// DownstreamResumeRequestExtensionFieldsは、ダウンストリーム再開要求に含まれる拡張フィールドです。
	DownstreamResumeRequestExtensionFields struct{}

	// DownstreamResumeResponseは、ダウンストリーム再開要求に対する応答です。
	DownstreamResumeResponse struct {
		RequestID                                                // リクエストID
		ResultCode      ResultCode                               // 結果コード
		ResultString    string                                   // 結果文字列
		ExtensionFields *DownstreamResumeResponseExtensionFields // 拡張フィールド
		ResumeToken     string                                   // Resumeトークン
	}

	// DownstreamResumeResponseExtensionFieldsは、ダウンストリーム再開要求に対する応答に含まれる拡張フィールドです。
	DownstreamResumeResponseExtensionFields struct{}

	// DownstreamCloseRequestは、ダウンストリーム切断要求です。
	DownstreamCloseRequest struct {
		RequestID                                              // リクエストID
		StreamID        uuid.UUID                              // ストリームID
		ExtensionFields *DownstreamCloseRequestExtensionFields // 拡張フィールド
	}
	// DownstreamCloseRequestExtensionFieldsは、ダウンストリーム切断要求に含まれる拡張フィールドです。
	DownstreamCloseRequestExtensionFields struct{}

	// DownstreamCloseResponseは、ダウンストリーム切断要求に対する応答です。
	DownstreamCloseResponse struct {
		RequestID                                               // リクエストID
		ResultCode      ResultCode                              // 結果コード
		ResultString    string                                  // 結果文字列
		ExtensionFields *DownstreamCloseResponseExtensionFields // 拡張フィールド
	}

	// DownstreamCloseResponseExtensionFieldsは、ダウンストリーム切断要求に対する応答に含まれる拡張フィールドです。
	DownstreamCloseResponseExtensionFields struct{}
)

func (_ *DownstreamOpenRequest) isMessage() {}

func (_ *DownstreamOpenResponse) isMessage() {}

func (_ *DownstreamResumeRequest) isMessage() {}

func (_ *DownstreamResumeResponse) isMessage() {}

func (_ *DownstreamCloseRequest) isMessage() {}

func (_ *DownstreamCloseResponse) isMessage() {}

func (r *DownstreamOpenResponse) ServerTimeOrUnixZero() int64 {
	return orUnixZero(r.ServerTime)
}
