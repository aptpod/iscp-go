package encoding_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/aptpod/iscp-go/encoding"
	"github.com/aptpod/iscp-go/encoding/json"
	"github.com/aptpod/iscp-go/encoding/protobuf"
	"github.com/aptpod/iscp-go/message"
)

func BenchmarkProtobuf(b *testing.B) {
	testee := protobuf.NewEncoding()
	doBenchmark(b, testee)
}

func BenchmarkJSON____(b *testing.B) {
	testee := json.NewEncoding()
	doBenchmark(b, testee)
}

func doBenchmark(b *testing.B, testee encoding.Encoding) {
	cases := []struct {
		name string
		in   message.Message
	}{
		{
			name: testnameDeco("ping"),
			in:   getPing(),
		},
		{
			name: testnameDeco("pong"),
			in:   getPong(),
		},
	}

	// encode
	for _, c := range cases {
		// setup
		buf := bytes.NewBuffer(make([]byte, 0, 8192))

		b.Run(c.name+"/encode", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				b.StartTimer()
				testee.EncodeTo(buf, c.in)
				b.StopTimer()
				testee.DecodeFrom(buf)
			}
		})
	}

	// decode
	for _, c := range cases {
		// setup
		buf := bytes.NewBuffer(make([]byte, 0, 8192))

		b.Run(c.name+"/decode", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				testee.EncodeTo(buf, c.in)
				b.StartTimer()
				testee.DecodeFrom(buf)
				b.StopTimer()
			}
		})
	}
}

func testnameDeco(s string) string {
	var b strings.Builder
	b.WriteString(s)
	b.WriteString(strings.Repeat(" ", 40-len(s)))
	return b.String()
}
