package transport_test

import (
	"testing"

	"github.com/aptpod/iscp-go/v2/message"
	"github.com/aptpod/iscp-go/v2/transport"
	"github.com/aptpod/iscp-go/v2/transport/codec/protobuf"
)

func BenchmarkMessageTransport_WriteMessage(b *testing.B) {
	r, w := transport.Pipe()
	defer r.Close()
	defer w.Close()

	mt := transport.NewMessageTransport(&transport.MessageTransportConfig{
		Transport: w,
		Codec:     protobuf.NewEncoding(),
	})

	msg := &message.Ping{RequestID: 1}

	// drain reader
	go func() {
		for {
			if _, err := r.Read(); err != nil {
				return
			}
		}
	}()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = mt.WriteMessage(msg)
	}
}

func BenchmarkMessageTransport_ReadMessage(b *testing.B) {
	r, w := transport.Pipe()
	defer r.Close()
	defer w.Close()

	mtw := transport.NewMessageTransport(&transport.MessageTransportConfig{
		Transport: w,
		Codec:     protobuf.NewEncoding(),
	})
	mtr := transport.NewMessageTransport(&transport.MessageTransportConfig{
		Transport: r,
		Codec:     protobuf.NewEncoding(),
	})

	msg := &message.Ping{RequestID: 1}

	// feed writer
	go func() {
		for {
			if err := mtw.WriteMessage(msg); err != nil {
				return
			}
		}
	}()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = mtr.ReadMessage()
	}
}
