package wire_test

import (
	"github.com/aptpod/iscp-go/encoding/protobuf"
	"github.com/aptpod/iscp-go/transport"
	"github.com/aptpod/iscp-go/wire"
)

func Pipe() (srv wire.EncodingTransport, cli wire.EncodingTransport) {
	return PipeWithSize(0, 0)
}

func PipeWithSize(srvMaxMessageSize, cliMaxMessageSize int64) (srv wire.EncodingTransport, cli wire.EncodingTransport) {
	srvtr, clitr := transport.Pipe()
	srv = transport.NewMessageTransport(&transport.MessageTransportConfig{
		Transport:      srvtr,
		Codec:          protobuf.NewEncoding(),
		MaxMessageSize: srvMaxMessageSize,
	})
	cli = transport.NewMessageTransport(&transport.MessageTransportConfig{
		Transport:      clitr,
		Codec:          protobuf.NewEncoding(),
		MaxMessageSize: cliMaxMessageSize,
	})
	return
}
