package transport_test

import (
	"testing"

	"github.com/aptpod/iscp-go/v2/encoding/protobuf"
	"github.com/aptpod/iscp-go/v2/message"
	"github.com/aptpod/iscp-go/v2/transport"
	"github.com/stretchr/testify/require"
)

// TestMessageTransport_EncodeMessage_WriteEncodedMessage は、符号化と書き込みを
// 分離した場合でも WriteMessage と同様にメッセージが届き、送信カウンタが
// 「書き込み成功時点」で加算されることを検証する。
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
	// 符号化だけでは送信カウンタは進まない。
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

// TestMessageTransport_WriteEncodedMessage_WriteError は、書き込みに失敗した
// 場合に送信カウンタが加算されないことを検証する。
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
