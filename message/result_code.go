package message

/*
ResultCode は、要求の処理結果を表す識別コードです。
*/
type ResultCode int32

/*
ResultCode は、以下の値を取ります。
*/
const (
	// 正常コード (0x00 - 0x3F)
	ResultCodeSucceeded            ResultCode = 0x00 // 処理が正常に成功したことを表します。
	ResultCodeNormalClosure        ResultCode = 0x00 // 正常にコネクションが閉じられたことを表します。
	ResultCodeIncompatibleVersion  ResultCode = 0x01 // ノードとブローカーのバージョンに互換性が無いことを表します。
	ResultCodeMaximumDataIDAlias   ResultCode = 0x02 // データIDエイリアス値の数が上限に達し、データIDエイリアス値を新たに割り当てることができないことを表します。
	ResultCodeMaximumUpstreamAlias ResultCode = 0x03 // アップストリームエイリアス値の数が上限に達し、アップストリームエイリアス値を新たに割り当てることができないことを表します。

	// 異常コード (0x40 - 0x7F)
	ResultCodeUnspecifiedError       ResultCode = 0x40 // 種類を規定しないエラーです。予期しないエラーが発生した場合に使用されます。
	ResultCodeNoNodeID               ResultCode = 0x41 // 接続時にノードIDを指定していないことを表します。
	ResultCodeAuthFailed             ResultCode = 0x42 // 認証や認可の処理に失敗したことを表します。
	ResultCodeConnectTimeout         ResultCode = 0x43 // 妥当な時間までに、通信の開始シーケンスが完了しなかったことを表します。
	ResultCodeMalformedMessage       ResultCode = 0x44 // 不正な形式のメッセージを受信したことを表します。
	ResultCodeProtocolError          ResultCode = 0x45 // プロトコル違反を表します。
	ResultCodeAckTimeout             ResultCode = 0x46 // ACKの返却までに時間がかかりすぎて、送信側よりネットワークが切断されたことを表します。
	ResultCodeInvalidPayload         ResultCode = 0x47 // ペイロードの形式が不正であることを表します。
	ResultCodeInvalidDataID          ResultCode = 0x48 // データIDが不正であることを表します。
	ResultCodeInvalidDataIDAlias     ResultCode = 0x49 // データIDエイリアスが不正であることを表します。
	ResultCodeInvalidDataFilter      ResultCode = 0x4A // データフィルタが不正であることを表します。
	ResultCodeStreamNotFound         ResultCode = 0x4B // 受信者が保持している情報の中に、対象のストリームが含まれないことを表します。
	ResultCodeResumeRequestConflict  ResultCode = 0x4C // 再開しようとしたストリームが接続中であることを表します。
	ResultCodeProcessFailed          ResultCode = 0x4D // 処理が失敗したことを表します。
	ResultCodeDesiredQosNotSupported ResultCode = 0x4E // 要求されたQoSをサポートしていないことを表します。
	// Deprecated: iSCPv2 v4.0.0以降では使用されません。
	ResultCodePingTimeout              ResultCode = 0x4F // Pingのタイムアウトが発生したことを表します。
	ResultCodeTooLargeMessageSize      ResultCode = 0x50 // メッセージのサイズが大きすぎることを表します。
	ResultCodeTooManyDataIDAliases     ResultCode = 0x51 // データIDエイリアスが多すぎることを表します。
	ResultCodeTooManyStreams           ResultCode = 0x52 // ストリームが多すぎることを表します。
	ResultCodeTooLongAckInterval       ResultCode = 0x53 // ACKの返却間隔が長すぎることを表します。
	ResultCodeTooManyDownstreamFilters ResultCode = 0x54 // ダウンストリームフィルタが多すぎることを表します。
	ResultCodeTooManyDataFilters       ResultCode = 0x55 // データフィルタが多すぎることを表します。
	ResultCodeTooLongExpiryInterval    ResultCode = 0x56 // 有効期限が長すぎることを表します。
	// Deprecated: iSCPv2 v4.0.0以降では使用されません。
	ResultCodeTooLongPingTimeout ResultCode = 0x57 // Pingタイムアウト値が大きすぎることを表します。
	// Deprecated: iSCPv2 v4.0.0以降では使用されません。
	ResultCodeTooShortPingInterval ResultCode = 0x58 // Ping間隔が短すぎることを表します。
	// Deprecated: iSCPv2 v4.0.0以降では使用されません。
	ResultCodeTooShortPingTimeout ResultCode = 0x59 // Pingタイムアウトが短すぎることを表します。
	ResultCodeRateLimitReached    ResultCode = 0x5A // レートリミットに到達したことを表します。
	ResultCodeTooLargeFeedID      ResultCode = 0x5B // フィードIDが大きすぎることを表します。
	ResultCodeTooManyTargetNodes  ResultCode = 0x5C // 対象ノードが多すぎることを表します。
	ResultCodeFeedNotFound        ResultCode = 0x5D // フィードが見つからなかったことを表します。
	ResultCodeInvalidResumeToken  ResultCode = 0x5E // Resumeトークンが不正または未指定であることを表します。

	// 拡張仕様コード (0x80 - 0xFF)
	ResultCodeNodeIDMismatch       ResultCode = 0x80 // すでに永続化されているセッションの生成元ノードと、新たに永続化しようとするノードが異なることを表します。
	ResultCodeSessionNotFound      ResultCode = 0x81 // セッションが見つからなかったことを表します。
	ResultCodeSessionAlreadyClosed ResultCode = 0x82 // セッションがすでに閉じられていることを表します。
	ResultCodeSessionCannotClosed  ResultCode = 0x83 // セッションを閉じることができないことを表します。
)
