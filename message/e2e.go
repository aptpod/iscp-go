package message

type (
	// DownstreamCallは、ダウンストリームコールです。
	//
	// ノード同士がエンドツーエンドでコミュニケーションをするためのメッセージです。
	DownstreamCall struct {
		CallID          string                         // コールID
		RequestCallID   string                         // リクエストコールID
		SourceNodeID    string                         // 送信元ノードID
		Name            string                         // 名称
		Type            string                         // 型
		Payload         []byte                         // ペイロード
		ExtensionFields *DownstreamCallExtensionFields // 拡張フィールド
	}

	// DownstreamCallExtensionFieldsは、ダウンストリームコールに含まれる拡張フィールドです。
	DownstreamCallExtensionFields struct{}

	// UpstreamCallは、アップストリームコールです。
	//
	// ノード同士がエンドツーエンドでコミュニケーションをするためのメッセージです。
	UpstreamCall struct {
		CallID            string                       // コールID
		RequestCallID     string                       // リクエストコールID
		DestinationNodeID string                       // 宛先ノードID
		Name              string                       // 名称
		Type              string                       // 型
		Payload           []byte                       // ペイロード
		ExtensionFields   *UpstreamCallExtensionFields // 拡張フィールド
	}
	// UpstreamCallExtensionFieldsは、アップストリームコールに含まれる拡張フィールドです。
	UpstreamCallExtensionFields struct{}

	// UpstreamCallAckは、アップストリームコールに対する応答です。
	UpstreamCallAck struct {
		CallID          string                          // コールID
		ResultCode      ResultCode                      // 結果コード
		ResultString    string                          // 結果文字列
		ExtensionFields *UpstreamCallAckExtensionFields // 拡張フィールド
	}

	// UpstreamCallAckExtensionFieldsは、アップストリームコールに対する応答に含まれる拡張フィールドです。
	UpstreamCallAckExtensionFields struct{}
)

func (*UpstreamCall) isMessage() {}

func (*DownstreamCall) isMessage() {}

func (*UpstreamCallAck) isMessage() {}
