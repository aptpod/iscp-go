package transport_test

import (
	"testing"

	"github.com/aptpod/iscp-go/v2/encoding/protobuf"
	"github.com/aptpod/iscp-go/v2/message"
	"github.com/aptpod/iscp-go/v2/transport"
	"github.com/stretchr/testify/require"
)

// TestMessageTransport_EncodeMessage_WriteEncodedMessage verifies that, even when
// encoding and writing are separated, the message is delivered as with WriteMessage
// and the send counter is incremented at the time the write succeeds.
func TestMessageTransport_EncodeMessage_WriteEncodedMessage(t *testing.T) {
	cli, srv := transport.Pipe()
	defer cli.Close()
	defer srv.Close()

	mt := transport.NewMessageTransport(&transport.MessageTransportConfig{
		Transport: cli,
		Encoding:  protobuf.NewEncoding(),
	})
	srvMt := transport.NewMessageTransport(&transport.MessageTransportConfig{
		Transport: srv,
		Encoding:  protobuf.NewEncoding(),
	})

	em, err := mt.EncodeMessage(&message.Ping{})
	require.NoError(t, err)
	// Encoding alone does not advance the send counter.
	require.Equal(t, uint64(0), mt.TxMessageCount())

	got := make(chan message.Message, 1)
	go func() {
		m, readErr := srvMt.ReadMessage()
		require.NoError(t, readErr)
		got <- m
	}()

	require.NoError(t, mt.WriteEncodedMessage(em))
	require.Equal(t, uint64(1), mt.TxMessageCount())
	require.IsType(t, &message.Ping{}, <-got)
}

// TestMessageTransport_WriteEncodedMessage_WriteError verifies that the send counter
// is not incremented when the write fails.
func TestMessageTransport_WriteEncodedMessage_WriteError(t *testing.T) {
	cli, srv := transport.Pipe()
	defer srv.Close()

	mt := transport.NewMessageTransport(&transport.MessageTransportConfig{
		Transport: cli,
		Encoding:  protobuf.NewEncoding(),
	})

	em, err := mt.EncodeMessage(&message.Ping{})
	require.NoError(t, err)

	require.NoError(t, cli.Close())
	require.Error(t, mt.WriteEncodedMessage(em))
	require.Equal(t, uint64(0), mt.TxMessageCount())
}
