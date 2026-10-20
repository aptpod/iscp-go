package webtransport

import "sync/atomic"

type datagram struct {
	t      *Transport
	rx, tx uint64
}

// Read reads a message from the transport.
func (d *datagram) Read() ([]byte, error) {
	r, err := d.t.ReadUnreliable()
	if err != nil {
		return nil, err
	}
	atomic.AddUint64(&d.rx, uint64(len(r)))
	return r, nil
}

// RxBytesCounterValue returns the current value of the received message counter.
func (d *datagram) RxBytesCounterValue() uint64 {
	return atomic.LoadUint64(&d.rx)
}

// Write writes a message to the transport.
func (d *datagram) Write(m []byte) error {
	if err := d.t.WriteUnreliable(m); err != nil {
		return err
	}
	atomic.AddUint64(&d.tx, uint64(len(m)))
	return nil
}

// Close closes the connection of the transport.
func (d *datagram) Close() error {
	// nop
	return nil
}

// TxBytesCounterValue returns the current value of the sent byte counter.
func (d *datagram) TxBytesCounterValue() uint64 {
	return atomic.LoadUint64(&d.tx)
}

func (d *datagram) IsUnreliable() {}
