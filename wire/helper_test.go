package wire_test

import (
	"github.com/aptpod/iscp-go/encoding"
	"github.com/aptpod/iscp-go/encoding/protobuf"
	"github.com/aptpod/iscp-go/transport"
	"github.com/aptpod/iscp-go/wire"
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
