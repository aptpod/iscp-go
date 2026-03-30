package iscp_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/aptpod/iscp-go/v2/errors"
	. "github.com/aptpod/iscp-go/v2/iscp"
	"github.com/aptpod/iscp-go/v2/message"
	"github.com/aptpod/iscp-go/v2/transport"
	"github.com/aptpod/iscp-go/v2/encoding/json"
	"github.com/aptpod/iscp-go/v2/encoding/protobuf"
)

func Pipe() (srv *transport.MessageTransport, cli *transport.MessageTransport) {
	return PipeWithSize(0, 0)
}

func PipeWithSize(srvMaxMessageSize, cliMaxMessageSize int64) (srv *transport.MessageTransport, cli *transport.MessageTransport) {
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

func Copy(dst *transport.MessageTransport, src *transport.MessageTransport) error {
	for {
		msg, err := src.ReadMessage()
		if err != nil {
			if errors.Is(err, transport.EOF) {
				return nil
			}
			if errors.Is(err, errors.ErrConnectionClosed) {
				return nil
			}
			return err
		}
		if err := dst.WriteMessage(msg); err != nil {
			if errors.Is(err, errors.ErrConnectionClosed) {
				return nil
			}
			return err
		}
	}
}

func mustRead(t *testing.T, tr *transport.MessageTransport, ignores ...message.Message) message.Message {
	for {
		msg, err := tr.ReadMessage()
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

func mustWrite(t *testing.T, tr *transport.MessageTransport, msg message.Message) {
	require.NoError(t, tr.WriteMessage(msg))
}

func mockConnectRequestWithVersion(t *testing.T, srv *transport.MessageTransport, version string) {
	msg, err := srv.ReadMessage()
	require.NoError(t, err)
	t.Log(msg)
	require.NoError(t, srv.WriteMessage(&message.ConnectResponse{
		RequestID:       0,
		ProtocolVersion: version,
		ResultCode:      message.ResultCodeSucceeded,
		ResultString:    "",
		ExtensionFields: &message.ConnectResponseExtensionFields{},
	}))
}

func mockConnectRequest(t *testing.T, srv *transport.MessageTransport) {
	mockConnectRequestWithVersion(t, srv, "3.0.0")
}

func mockConnectRequestV4(t *testing.T, srv *transport.MessageTransport) {
	mockConnectRequestWithVersion(t, srv, "4.0.0")
}

var TransportTest TransportName = "test"

var (
	_ transport.Dialer    = (*dialer)(nil)
	_ transport.Transport = (*dialer)(nil)
)

type dialer struct {
	transport.ReadWriter
	srv               *transport.MessageTransport
	negotiationParams transport.NegotiationParams
}

// CloseWithStatus implements transport.Closer.
func (d *dialer) CloseWithStatus(transport.CloseStatus) error {
	return d.Close()
}

func newDialer(p transport.NegotiationParams) *dialer {
	cli, srv := transport.Pipe()
	enc := protobuf.NewEncoding()
	if p.Encoding == transport.EncodingNameJSON {
		enc = json.NewEncoding()
	}
	return &dialer{
		ReadWriter: cli,
		srv: transport.NewMessageTransport(&transport.MessageTransportConfig{
			Transport: srv,
			Codec:     enc,
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

// Nameはトランスポート名を返却します。
func (d *dialer) Name() transport.Name {
	return transport.Name(TransportTest)
}
