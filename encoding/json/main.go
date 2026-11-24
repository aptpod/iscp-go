/*
Package json は、 JSON フォーマットを使用したエンコーディングを提供するパッケージです。
*/
package json

import (
	"io"

	autogen "github.com/aptpod/iscp-proto/gen/go/iscp2/v1"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/aptpod/iscp-go/v2/encoding/convert"
	"github.com/aptpod/iscp-go/v2/errors"
	"github.com/aptpod/iscp-go/v2/message"
	"github.com/aptpod/iscp-go/v2/transport"
)

type encoder struct{}

func (e *encoder) ContentType() transport.ContentType {
	return transport.ContentTypeText
}

func (e *encoder) Name() transport.EncodingName {
	return transport.EncodingNameJSON
}

/*
NewEncoding は、 JSON フォーマット用エンコーディングを生成します。
*/
func NewEncoding() transport.Encoding {
	return &encoder{}
}

var marshaler = protojson.MarshalOptions{
	EmitUnpopulated: true,
	UseProtoNames:   true,
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

	bs, err := marshaler.Marshal(pb)
	if err != nil {
		return 0, err
	}

	return wr.Write(bs)
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

	bs, err := io.ReadAll(rd)
	if err != nil {
		return 0, nil, err
	}
	var pb autogen.Message
	if err := protojson.Unmarshal(bs, &pb); err != nil {
		return 0, nil, err
	}
	res, err := convert.ProtoToWire(&pb)
	if err != nil {
		return 0, nil, err
	}

	return len(bs), res, nil
}
