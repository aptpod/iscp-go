package message

/*
ResultCode は、要求の処理結果を表す識別コードです。
*/
type ResultCode int32

/*
ResultCode は、以下の値を取ります。
*/
const (
	_ ResultCode = iota

	ResultCodeSucceeded                // 処理が正常に成功したことを表します。
	ResultCodeNormalClosure            // 正常にコネクションが閉じられたことを表します。
	ResultCodeIncompatibleVersion      // ノードとブローカーのバージョンに互換性が無いことを表します。
	ResultCodeMaximumDataIDAlias       // データIDエイリアス値の数が上限に達し、データIDエイリアス値を新たに割り当てることができないことを表します。
	ResultCodeMaximumUpstreamAlias     // アップストリームエイリアス値の数が上限に達し、アップストリームエイリアス値を新たに割り当てることができないことを表します。
	ResultCodeUnspecifiedError         // 種類を規定しないエラーです。予期しないエラーが発生した場合に使用されます。
	ResultCodeNoNodeID                 // 接続時にノードIDを指定していないことを表します。
	ResultCodeAuthFailed               // 認証や認可の処理に失敗したことを表します。
	ResultCodeConnectTimeout           // 妥当な時間までに、通信の開始シーケンスが完了しなかったことを表します。
	ResultCodeMalformedMessage         // 不正な形式のメッセージを受信したことを表します。
	ResultCodeProtocolError            // プロトコル違反を表します。
	ResultCodeAckTimeout               // ACKの返却までに時間がかかりすぎて、送信側よりネットワークが切断されたことを表します。
	ResultCodeInvalidPayload           // ペイロードの形式が不正であることを表します。
	ResultCodeInvalidDataID            // データIDが不正であることを表します。
	ResultCodeInvalidDataIDAlias       // データIDエイリアスが不正であることを表します。
	ResultCodeInvalidDataFilter        // データフィルタが不正であることを表します。
	ResultCodeStreamNotFound           // 受信者が保持している情報の中に、対象のストリームが含まれないことを表します。
	ResultCodeResumeRequestConflict    // 再開しようとしたストリームが接続中であることを表します。
	ResultCodeProcessFailed            // 処理が失敗したことを表します。
	ResultCodeDesiredQosNotSupported   // 要求されたQoSをサポートしていないことを表します。
	ResultCodePingTimeout              // Pingのタイムアウトが発生したことを表します。
	ResultCodeTooLargeMessageSize      // メッセージのサイズが大きすぎることを表します。
	ResultCodeTooManyDataIDAliases     // データIDエイリアスが多すぎることを表します。
	ResultCodeTooManyStreams           // ストリームが多すぎることを表します。
	ResultCodeTooLongAckInterval       // ACKの返却間隔が長すぎることを表します。
	ResultCodeTooManyDownstreamFilters // ダウンストリームフィルタが多すぎることを表します。
	ResultCodeTooManyDataFilters       // データフィルタが多すぎることを表します。
	ResultCodeTooLongExpiryInterval    // 有効期限が長すぎることを表します。
	ResultCodeTooLongPingTimeout       // Pingタイムアウト値が大きすぎることを表します。
	ResultCodeTooShortPingInterval     // Ping間隔が短すぎることを表します。
	ResultCodeTooShortPingTimeout      // Pingタイムアウトが短すぎることを表します。
	ResultCodeNodeIDMismatch           // すでに永続化されているセッションの生成元ノードと、新たに永続化しようとするノードが異なることを表します。
	ResultCodeRateLimitReached         // レートリミットに到達したことを表します。
	ResultCodeSessionNotFound          // セッションが見つからなかったことを表します。
	ResultCodeSessionAlreadyClosed     // セッションがすでに閉じられていることを表します。
	ResultCodeSessionCannotClosed      // セッションを閉じることができないことを表します。
	ResultCodeInvalidResumeToken       // Resumeトークンが不正または未指定であることを表します。
)
