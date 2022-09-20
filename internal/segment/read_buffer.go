package segment

import (
	"encoding/binary"
	"sync"
	"time"
)

type ReadBuffers struct {
	sync.Mutex
	ReadBuffer       map[uint32]*ReadBuffer
	ReadBufferExpiry time.Duration
}

func (t *ReadBuffers) Receive(bs []byte) ([]byte, bool, error) {
	t.Lock()
	defer t.Unlock()

	seqNum := binary.BigEndian.Uint32(bs[:4])
	segIdx := binary.BigEndian.Uint16(bs[6:8])

	buf, ok := t.ReadBuffer[seqNum]
	if !ok {
		maxSegIdx := binary.BigEndian.Uint16(bs[4:6])
		t.ReadBuffer[seqNum] = &ReadBuffer{
			SegCount: 0,
			MsgSize:  0,
			Msgs:     make([][]byte, maxSegIdx+1),
		}
		buf = t.ReadBuffer[seqNum]
	}
	buf.ExpiredAt = timeNow().Add(t.ReadBufferExpiry)
	m, ok := buf.add(int(segIdx), bs[8:])
	if !ok {
		return nil, false, nil
	}
	delete(t.ReadBuffer, seqNum)
	return m, true, nil
}

func (b *ReadBuffers) RemoveExpired() {
	b.Lock()
	defer b.Unlock()
	now := timeNow()
	for k, v := range b.ReadBuffer {
		if now.After(v.ExpiredAt) {
			delete(b.ReadBuffer, k)
		}
	}
}

type ReadBuffer struct {
	SegCount  int
	MsgSize   int
	Msgs      [][]byte
	ExpiredAt time.Time
}

func (b *ReadBuffer) add(segIdx int, bs []byte) ([]byte, bool) {
	if len(b.Msgs) <= segIdx {
		// TODO invalid data format. handling error.
		return nil, false
	}
	b.SegCount++
	b.MsgSize += len(bs)
	b.Msgs[segIdx] = bs
	if len(b.Msgs) == b.SegCount {
		return b.build(), true
	}
	return nil, false
}

func (b *ReadBuffer) build() []byte {
	res := make([]byte, 0, b.MsgSize)
	for _, v := range b.Msgs {
		res = append(res, v...)
	}
	return res
}
