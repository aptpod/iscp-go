package quic

import (
	"sync/atomic"

	"github.com/aptpod/iscp-go/v2/internal/segment"
	"github.com/aptpod/iscp-go/v2/transport"
)

type datagram struct {
	t      *Transport
	rx, tx uint64
}

// Read reads a message from the transport.
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

// RxBytesCounterValue returns the current value of the received message counter.
func (d *datagram) RxBytesCounterValue() uint64 {
	return atomic.LoadUint64(&d.rx)
}

// Write writes a message to the transport.
func (d *datagram) Write(m []byte) error {
	encodedMessage, err := d.t.encodeFunc(m, d.t.compressConfig.Level)
	if err != nil {
		if isErrTransportClosed(err) {
			return transport.ErrAlreadyClosed
		}
		return err
	}
	n, err := segment.SendTo(d.t.quicSession, atomic.AddUint32(&d.t.sequenceNumber, 1), encodedMessage)
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
