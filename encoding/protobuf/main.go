/*
Package protobuf は、 Protocol Buffers を使用したエンコーディングを提供するパッケージです。
*/
package protobuf

import (
	"bytes"
	"io"
	"sync"

	autogen "github.com/aptpod/iscp-proto/gen/gogofast/iscp2/v1"
	"github.com/gogo/protobuf/proto"

	"github.com/aptpod/iscp-go/encoding"
	"github.com/aptpod/iscp-go/encoding/convert"
	"github.com/aptpod/iscp-go/errors"
	"github.com/aptpod/iscp-go/message"
)

type encoder struct{}

/*
NewEncoding は、 Protocol Buffers 用エンコーディングを生成します。
*/
func NewEncoding() encoding.Encoding {
	return &encoder{}
}

func (e *encoder) Name() encoding.Name {
	return encoding.NameProtobuf
}

func (e *encoder) ContentType() encoding.ContentType {
	return encoding.ContentTypeBinary
}

const bufferSize = 4096

var globalEncodeBufferPool = sync.Pool{
	New: func() interface{} {
		return proto.NewBuffer(make([]byte, 0, bufferSize))
	},
}

func (e *encoder) EncodeTo(wr io.Writer, m message.Message) (n int, er error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if err, ok := recovered.(error); ok {
				er = err
			}
			er = errors.Errorf("%v", recovered)
		}
	}()

	// NOTE: reuse encoder buffer
	buf := globalEncodeBufferPool.Get().(*proto.Buffer)
	defer func() {
		buf.Reset()
		globalEncodeBufferPool.Put(buf)
	}()

	pb, err := convert.WireToProto(m)
	if err != nil {
		return 0, err
	}

	if err := buf.Marshal(pb); err != nil {
		return 0, err
	}

	return wr.Write(buf.Bytes())
}

var bufferPool = sync.Pool{New: func() interface{} {
	return bytes.NewBuffer(nil)
}}

func (e *encoder) DecodeFrom(rd io.Reader) (n int, m message.Message, er error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if err, ok := recovered.(error); ok {
				er = err
			}
			er = errors.Errorf("%v", recovered)
		}
	}()

	var pb autogen.Message
	buffer := bufferPool.Get().(*bytes.Buffer)
	defer func() {
		buffer.Reset()
		bufferPool.Put(buffer)
	}()
	if _, err := io.Copy(buffer, rd); err != nil {
		return 0, nil, err
	}

	if err := pb.Unmarshal(buffer.Bytes()); err != nil {
		return 0, nil, err
	}

	res, err := convert.ProtoToWire(&pb)
	if err != nil {
		return 0, nil, err
	}
	return buffer.Len(), res, nil
}
