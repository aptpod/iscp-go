package segment

import (
	"encoding/binary"
	"math"

	"github.com/aptpod/iscp-go/errors"
	"github.com/aptpod/iscp-go/transport"
)

type Sender interface {
	SendDatagram([]byte) error
}

func SendTo(wr Sender, seqNum uint32, msgPayload []byte) (int, error) {
	if len(msgPayload) <= maxPayloadSize {
		return send(wr, seqNum, 0, 0, msgPayload)
	}
	var offset int
	maxSegIdx := len(msgPayload) / maxPayloadSize
	if maxSegIdx > math.MaxUint16 {
		return 0, errors.Errorf("MaxSegmentIndex must be less than  %v : %w", math.MaxUint16, transport.ErrInvalidMessage)
	}
	var size int

	for segIdx := 0; segIdx <= maxSegIdx; segIdx++ {
		offset = segIdx * maxPayloadSize
		var payload []byte
		if segIdx == maxSegIdx {
			payload = msgPayload[offset:]
		} else {
			payload = msgPayload[offset : offset+maxPayloadSize]
		}
		n, err := send(wr, seqNum, uint16(segIdx), uint16(maxSegIdx), payload)
		if err != nil {
			return 0, err
		}
		size += n
	}
	return size, nil
}

func send(wr Sender, seqNum uint32, segIdx, maxIdx uint16, msgPayload []byte) (int, error) {
	bs := make([]byte, 8, 8+len(msgPayload))
	binary.BigEndian.PutUint32(bs[:4], seqNum)
	binary.BigEndian.PutUint16(bs[4:6], maxIdx)
	binary.BigEndian.PutUint16(bs[6:8], segIdx)
	bs = append(bs, msgPayload...)

	if err := wr.SendDatagram(bs); err != nil {
		return 0, err
	}
	return len(bs), nil
}
