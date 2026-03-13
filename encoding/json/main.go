/*
Package json は、 JSON フォーマットを使用したエンコーディングを提供するパッケージです。
*/
package json

import (
	"bytes"
	"io"

	autogen "github.com/aptpod/iscp-proto/gen/gogofast/iscp2/v1"
	"github.com/gogo/protobuf/jsonpb"

	"github.com/aptpod/iscp-go/encoding"
	"github.com/aptpod/iscp-go/encoding/convert"
	"github.com/aptpod/iscp-go/errors"
	"github.com/aptpod/iscp-go/internal/xio"
	"github.com/aptpod/iscp-go/message"
)

type encoder struct{}

func (e *encoder) ContentType() encoding.ContentType {
	return encoding.ContentTypeText
}

func (e *encoder) Name() encoding.Name {
	return encoding.NameJSON
}

/*
NewEncoding は、 JSON フォーマット用エンコーディングを生成します。
*/
func NewEncoding() encoding.Encoding {
	return &encoder{}
}

var marshaler = jsonpb.Marshaler{
	EmitDefaults: true,
	OrigName:     true,
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

	pb, err := convert.WireToProto(m)
	if err != nil {
		return 0, err
	}
	// defer convert.FreeMessage(pb)

	var buf bytes.Buffer
	if err := marshaler.Marshal(&buf, pb); err != nil {
		return 0, err
	}
	writtenBytes := buf.Len()

	if _, err := io.Copy(wr, &buf); err != nil {
		return 0, err
	}

	return writtenBytes, nil
}

func (e *encoder) DecodeFrom(rd io.Reader) (n int, m message.Message, er error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if err, ok := recovered.(error); ok {
				er = err
			}
			er = errors.Errorf("%v", recovered)
		}
	}()

	ird := xio.NewCaptureReader(rd)
	var pb autogen.Message
	if err := jsonpb.Unmarshal(ird, &pb); err != nil {
		return 0, nil, err
	}
	res, err := convert.ProtoToWire(&pb)
	if err != nil {
		return 0, nil, err
	}

	return ird.ReadBytes, res, nil
}
