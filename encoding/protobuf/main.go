/*
Package protobuf は、 Protocol Buffers を使用したエンコーディングを提供するパッケージです。
*/
package protobuf

import (
	"bytes"
	"io"
	"sync"

	autogen "github.com/aptpod/iscp-proto/gen/go/iscp2/v1"

	"github.com/aptpod/iscp-go/v2/encoding/convert"
	"github.com/aptpod/iscp-go/v2/errors"
	"github.com/aptpod/iscp-go/v2/message"
	"github.com/aptpod/iscp-go/v2/transport"
)

type encoder struct{}

/*
NewEncoding は、 Protocol Buffers 用エンコーディングを生成します。
*/
func NewEncoding() transport.Encoding {
	return &encoder{}
}

func (e *encoder) Name() transport.EncodingName {
	return transport.EncodingNameProtobuf
}

func (e *encoder) ContentType() transport.ContentType {
	return transport.ContentTypeBinary
}

const bufferSize = 4096

var globalEncodeBufferPool = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, 0, bufferSize)
		return &buf
	},
}

func (e *encoder) EncodeTo(wr io.Writer, m message.Message) (n int, er error) {
	// NOTE: reuse encoder buffer
	bufp := globalEncodeBufferPool.Get().(*[]byte)
	defer func() {
		globalEncodeBufferPool.Put(bufp)
		if recovered := recover(); recovered != nil {
			if err, ok := recovered.(error); ok {
				er = err
			} else {
				er = errors.Errorf("%v", recovered)
			}
		}
	}()

	pb, err := convert.WireToProto(m)
	if err != nil {
		return 0, err
	}

	size := pb.SizeVT()
	if cap(*bufp) < size {
		*bufp = make([]byte, size)
	}
	buf := (*bufp)[:size]
	written, err := pb.MarshalToSizedBufferVTStrict(buf)
	if err != nil {
		return 0, err
	}

	return wr.Write(buf[size-written:])
}

var bufferPool = sync.Pool{New: func() interface{} {
	return bytes.NewBuffer(nil)
}}

func (e *encoder) DecodeFrom(rd io.Reader) (n int, m message.Message, er error) {
	var pb autogen.Message
	buffer := bufferPool.Get().(*bytes.Buffer)
	defer func() {
		buffer.Reset()
		bufferPool.Put(buffer)
		if recovered := recover(); recovered != nil {
			if err, ok := recovered.(error); ok {
				er = err
			} else {
				er = errors.Errorf("%v", recovered)
			}
		}
	}()
	if _, err := io.Copy(buffer, rd); err != nil {
		return 0, nil, err
	}

	if err := pb.UnmarshalVT(buffer.Bytes()); err != nil {
		return 0, nil, err
	}

	res, err := convert.ProtoToWire(&pb)
	if err != nil {
		return 0, nil, err
	}
	return buffer.Len(), res, nil
}
