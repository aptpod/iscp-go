package message

/*
Message は、 iSCP で使用されるメッセージを表すインターフェースです。
*/
type Message interface {
	isMessage()
}

/*
Request は、 iSCP で使用されるリクエストメッセージを表すインターフェースです。
*/
type Request interface {
	Message
	GetRequestID() uint32
}

// RequestIDは、リクエストIDです。
//
// リクエストメッセージを識別するために使用します。
type RequestID uint32

// GetRequestIDは、リクエストIDを返却します。
func (i RequestID) GetRequestID() uint32 {
	return uint32(i)
}

/*
Stream は、 iSCP で使用されるストリームメッセージを表すインターフェースです。
*/
type Stream interface {
	Message
	isStream()
}
