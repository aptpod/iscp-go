package transport

import (
	"sync"
	"sync/atomic"

	"github.com/aptpod/iscp-go/errors"
)

type pipe struct {
	rx        chan []byte
	rxErr     chan error
	rxCounter *uint64

	tx        chan<- []byte
	txErr     chan error
	txCounter *uint64

	once           sync.Once
	closedCh       chan struct{}
	remoteClosedCh <-chan struct{}
}

func (p *pipe) Read() ([]byte, error) {
	select {
	case <-p.remoteClosedCh:
		return nil, EOF
	case <-p.closedCh:
		return nil, ErrAlreadyClosed
	case msg := <-p.rx:
		p.rxErr <- nil
		atomic.AddUint64(p.rxCounter, uint64(len(msg)))
		return msg, nil
	}
}

func (p *pipe) Write(message []byte) error {
	select {
	case <-p.remoteClosedCh:
		return ErrAlreadyClosed
	case <-p.closedCh:
		return ErrAlreadyClosed
	case p.tx <- message:
		atomic.AddUint64(p.txCounter, uint64(len(message)))
		return <-p.txErr
	}
}

func (p *pipe) RxBytesCounterValue() uint64 {
	return atomic.LoadUint64(p.rxCounter)
}

func (p *pipe) TxBytesCounterValue() uint64 {
	return atomic.LoadUint64(p.txCounter)
}

func (p *pipe) Close() error {
	p.once.Do(func() {
		close(p.closedCh)
	})
	return nil
}

func Pipe() (ReadWriter, ReadWriter) {
	ch1 := make(chan []byte)
	ch1err := make(chan error)

	ch2 := make(chan []byte)
	ch2err := make(chan error)

	chClosed1 := make(chan struct{})
	chClosed2 := make(chan struct{})

	return &pipe{
			rx:    ch2,
			rxErr: ch2err,

			tx:    ch1,
			txErr: ch1err,

			txCounter: func(u uint64) *uint64 { return &u }(0),
			rxCounter: func(u uint64) *uint64 { return &u }(0),

			once:           sync.Once{},
			closedCh:       chClosed1,
			remoteClosedCh: chClosed2,
		}, &pipe{
			rx:    ch1,
			rxErr: ch1err,

			tx:    ch2,
			txErr: ch2err,

			txCounter: func(u uint64) *uint64 { return &u }(0),
			rxCounter: func(u uint64) *uint64 { return &u }(0),

			once:           sync.Once{},
			closedCh:       chClosed2,
			remoteClosedCh: chClosed1,
		}
}

func Copy(dst ReadWriter, src ReadWriter) error {
	for {
		msg, err := src.Read()
		if err != nil {
			if errors.Is(err, EOF) {
				return nil
			}
			if errors.Is(err, ErrAlreadyClosed) {
				return nil
			}
			return err
		}
		if err := dst.Write(msg); err != nil {
			if errors.Is(err, ErrAlreadyClosed) {
				return nil
			}
			return err
		}
	}
}
