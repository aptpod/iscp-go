package wire_test

import (
	"github.com/aptpod/iscp-go/v2/encoding"
	"github.com/aptpod/iscp-go/v2/encoding/protobuf"
	"github.com/aptpod/iscp-go/v2/transport"
	"github.com/aptpod/iscp-go/v2/wire"
)

func Pipe() (srv wire.EncodingTransport, cli wire.EncodingTransport) {
	return PipeWithSize(0, 0)
}

func PipeWithSize(srvMaxMessageSize, cliMaxMessageSize encoding.Size) (srv wire.EncodingTransport, cli wire.EncodingTransport) {
	srvtr, clitr := transport.Pipe()
	srv = encoding.NewTransport(&encoding.TransportConfig{
		Transport:      srvtr,
		Encoding:       protobuf.NewEncoding(),
		MaxMessageSize: srvMaxMessageSize,
	})
	cli = encoding.NewTransport(&encoding.TransportConfig{
		Transport:      clitr,
		Encoding:       protobuf.NewEncoding(),
		MaxMessageSize: cliMaxMessageSize,
	})
	return
}
