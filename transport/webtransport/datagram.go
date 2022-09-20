package webtransport

import "sync/atomic"

type datagram struct {
	t      *Transport
	rx, tx uint64
}

// Read は、トランスポートからメッセージを読み出します。
func (d *datagram) Read() ([]byte, error) {
	r, err := d.t.ReadUnreliable()
	if err != nil {
		return nil, err
	}
	atomic.AddUint64(&d.rx, uint64(len(r)))
	return r, nil
}

// RxBytesCounterValue は、現在の受信メッセージカウンターの値を返します。
func (d *datagram) RxBytesCounterValue() uint64 {
	return atomic.LoadUint64(&d.rx)
}

// Write は、トランスポートへメッセージを書き込みます。
func (d *datagram) Write(m []byte) error {
	if err := d.t.WriteUnreliable(m); err != nil {
		return err
	}
	atomic.AddUint64(&d.tx, uint64(len(m)))
	return nil
}

// Close は、トランスポートのコネクションを切断します。
func (d *datagram) Close() error {
	// nop
	return nil
}

// TxBytesCounterValue は、現在の送信バイトカウンターの値を返します。
func (d *datagram) TxBytesCounterValue() uint64 {
	return atomic.LoadUint64(&d.tx)
}

func (d *datagram) IsUnreliable() {}
