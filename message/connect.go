package message

import (
	"time"

	uuid "github.com/google/uuid"
)

type (
	// ConnectRequestは、接続要求です。
	ConnectRequest struct {
		RequestID                                      // リクエストID
		ProtocolVersion string                         // プロトコルバージョン
		NodeID          string                         // ノードID
		PingInterval    time.Duration                  // Ping間隔
		PingTimeout     time.Duration                  // Pingタイムアウト
		ExtensionFields *ConnectRequestExtensionFields // 拡張フィールド
	}
	// ConnectRequestExtensionFieldsは、接続要求に含まれる拡張フィールドです。
	ConnectRequestExtensionFields struct {
		AccessToken string                  // アクセストークン
		Intdash     *IntdashExtensionFields // 拡張フィールド
	}

	// IntdashExtensionFieldsは、intdash API用の拡張フィールドです。
	IntdashExtensionFields struct {
		ProjectUUID uuid.UUID
	}

	// ConnectResponseは、接続要求に対する応答です。
	ConnectResponse struct {
		RequestID                                       // リクエストID
		ProtocolVersion string                          // プロトコルバージョン
		ResultCode      ResultCode                      // 結果コード
		ResultString    string                          // 結果文字列
		ExtensionFields *ConnectResponseExtensionFields // 拡張フィールド
	}

	// ConnectResponseExtensionFieldsは、接続要求に対する応答に含まれる拡張フィールドです。
	ConnectResponseExtensionFields struct{}

	// Disconnectは、切断です。
	Disconnect struct {
		ResultCode      ResultCode                 // 結果コード
		ResultString    string                     // 結果文字列
		ExtensionFields *DisconnectExtensionFields // 拡張フィールド
	}

	// DisconnectExtensionFieldsは、切断に含まれる拡張フィールドです。
	DisconnectExtensionFields struct{}
)

func (_ *ConnectRequest) isMessage() {}

// AccessTokenは、拡張フィールドのアクセストークンを返却します。
func (c *ConnectRequest) AccessToken() string {
	if c.ExtensionFields == nil {
		return ""
	}
	return c.ExtensionFields.AccessToken
}

func (_ *ConnectResponse) isMessage() {}

func (_ *Disconnect) isMessage() {}
