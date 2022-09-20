package quic

import (
	"sync/atomic"

	"github.com/aptpod/iscp-go/internal/segment"
	"github.com/aptpod/iscp-go/transport"
)

type datagram struct {
	t      *Transport
	rx, tx uint64
}

// Read は、トランスポートからメッセージを読み出します。
func (d *datagram) Read() ([]byte, error) {
	set, ok := <-d.t.readUnreliableC
	if !ok {
		return nil, transport.ErrAlreadyClosed
	}
	if err := set.err; err != nil {
		if isErrTransportClosed(err) {
			return nil, transport.ErrAlreadyClosed
		}
		return nil, err
	}
	atomic.AddUint64(&d.rx, uint64(len(set.msg)))
	return set.msg, nil
}

// RxBytesCounterValue は、現在の受信メッセージカウンターの値を返します。
func (d *datagram) RxBytesCounterValue() uint64 {
	return atomic.LoadUint64(&d.rx)
}

// Write は、トランスポートへメッセージを書き込みます。
func (d *datagram) Write(m []byte) error {
	m, err := d.t.encodeFunc(m, d.t.compressConfig.Level)
	if err != nil {
		if isErrTransportClosed(err) {
			return transport.ErrAlreadyClosed
		}
		return err
	}
	n, err := segment.SendTo(d.t.quicSession, atomic.AddUint32(&d.t.sequenceNumber, 1), m)
	if err != nil {
		if isErrTransportClosed(err) {
			return transport.ErrAlreadyClosed
		}
		return err
	}
	atomic.AddUint64(d.t.txBytesCounter, uint64(n))
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
