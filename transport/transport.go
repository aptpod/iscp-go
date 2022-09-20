package transport

// Readerはトランスポートからメッセージを読み出すインターフェースです。
type Reader interface {
	// Read は、トランスポートからメッセージを読み出します。
	Read() ([]byte, error)
	// Close は、トランスポートのコネクションを切断します。
	Close() error

	// RxBytesCounterValue は、現在の受信メッセージカウンターの値を返します。
	RxBytesCounterValue() uint64
}

// Writerはトランスポートからメッセージを書き込むインターフェースです。
type Writer interface {
	// Write は、トランスポートへメッセージを書き込みます。
	Write([]byte) error
	// Close は、トランスポートのコネクションを切断します。
	Close() error
	// TxBytesCounterValue は、現在の送信バイトカウンターの値を返します。
	TxBytesCounterValue() uint64
}

// ReadWriterはトランスポートからメッセージを読み書きのインターフェースです。
type ReadWriter interface {
	Reader
	Writer
}

/*
Transport は、 iSCP のトランスポート層を抽象化したインターフェースです。
*/
type Transport interface {
	ReadWriter

	// AsUnreliable は UnreliableTransportを返します。
	//
	// もし、 Unreliableをサポートしていない場合は okはfalseを返します。
	AsUnreliable() (tr UnreliableTransport, ok bool)

	// NegotiationParams は、トランスポートで事前ネゴシエーションされたパラメーターを返します。
	NegotiationParams() NegotiationParams
}

type UnreliableTransport interface {
	ReadWriter
	IsUnreliable()
}
