package iscp_test

import (
	"fmt"
	"testing"

	"github.com/aptpod/iscp-go/encoding"
	"github.com/aptpod/iscp-go/encoding/json"
	"github.com/aptpod/iscp-go/encoding/protobuf"
	"github.com/aptpod/iscp-go/errors"
	. "github.com/aptpod/iscp-go/iscp"
	"github.com/aptpod/iscp-go/message"
	"github.com/aptpod/iscp-go/transport"
	"github.com/aptpod/iscp-go/wire"
	"github.com/stretchr/testify/require"
)

func Pipe() (srv wire.Transport, cli wire.Transport) {
	return PipeWithSize(0, 0)
}

func PipeWithSize(srvMaxMessageSize, cliMaxMessageSize encoding.Size) (srv wire.Transport, cli wire.Transport) {
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

func Copy(dst wire.Transport, src wire.Transport) error {
	for {
		msg, err := src.Read()
		if err != nil {
			if errors.Is(err, transport.EOF) {
				return nil
			}
			if errors.Is(err, errors.ErrConnectionClosed) {
				return nil
			}
			return err
		}
		if err := dst.Write(msg); err != nil {
			if errors.Is(err, errors.ErrConnectionClosed) {
				return nil
			}
			return err
		}
	}
}

func mustRead(t *testing.T, tr wire.Transport, ignores ...message.Message) message.Message {
	for {
		msg, err := tr.Read()
		require.NoError(t, err)
		var ignore bool
		for _, v := range ignores {
			if fmt.Sprintf("%T", msg) == fmt.Sprintf("%T", v) {
				ignore = true
				break
			}
		}
		if ignore {
			continue
		}
		return msg
	}
}

func mustWrite(t *testing.T, tr wire.Transport, msg message.Message) {
	require.NoError(t, tr.Write(msg))
}

func mockConnectRequest(t *testing.T, srv wire.Transport) {
	msg, err := srv.Read()
	require.NoError(t, err)
	t.Log(msg)
	require.NoError(t, srv.Write(&message.ConnectResponse{
		RequestID:       0,
		ProtocolVersion: "",
		ResultCode:      message.ResultCodeSucceeded,
		ResultString:    "",
		ExtensionFields: &message.ConnectResponseExtensionFields{},
	}))
}

var TransportTest Transport = "test"

type dialer struct {
	transport.ReadWriter
	srv               wire.Transport
	negotiationParams transport.NegotiationParams
}

func newDialer(p transport.NegotiationParams) *dialer {
	cli, srv := transport.Pipe()
	enc := protobuf.NewEncoding()
	if p.Encoding == transport.EncodingJSON {
		enc = json.NewEncoding()
	}
	return &dialer{
		ReadWriter: cli,
		srv: encoding.NewTransport(&encoding.TransportConfig{
			Transport:      srv,
			Encoding:       enc,
			MaxMessageSize: 0,
		}),
		negotiationParams: p,
	}
}

func (d *dialer) Dial(c transport.DialConfig) (transport.Transport, error) {
	d.negotiationParams = c.NegotiationParams()
	return d, nil
}

// AsUnreliable は UnreliableTransportを返します。
//
// もし、 Unreliableをサポートしていない場合は okはfalseを返します。
func (d *dialer) AsUnreliable() (tr transport.UnreliableTransport, ok bool) {
	return nil, false
}

// NegotiationParams は、トランスポートで事前ネゴシエーションされたパラメーターを返します。
func (d *dialer) NegotiationParams() transport.NegotiationParams {
	return d.negotiationParams
}
