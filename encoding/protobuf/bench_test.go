package protobuf_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/google/uuid"

	. "github.com/aptpod/iscp-go/v2/encoding/protobuf"
	"github.com/aptpod/iscp-go/v2/message"
)

const benchDataPoints = 1000

func benchDataPointGroups() []*message.DataPointGroup {
	dps := make([]*message.DataPoint, benchDataPoints)
	for i := range dps {
		dps[i] = &message.DataPoint{ElapsedTime: time.Duration(i) * time.Millisecond, Payload: []byte{0, 1, 2, 3, 4, 5, 6, 7}}
	}
	return []*message.DataPointGroup{{DataIDOrAlias: message.DataIDAlias(1), DataPoints: dps}}
}

func benchMessages() []struct {
	name string
	msg  message.Message
} {
	return []struct {
		name string
		msg  message.Message
	}{
		{
			name: "UpstreamChunk",
			msg: &message.UpstreamChunk{
				StreamIDAlias:   1,
				StreamChunk:     &message.StreamChunk{SequenceNumber: 1, DataPointGroups: benchDataPointGroups()},
				ExtensionFields: &message.UpstreamChunkExtensionFields{},
			},
		},
		{
			name: "DownstreamChunk",
			msg: &message.DownstreamChunk{
				StreamIDAlias: 1,
				UpstreamOrAlias: &message.UpstreamInfo{
					SessionID:    "SessionID",
					SourceNodeID: "SourceNodeID",
					StreamID:     uuid.MustParse("22222222-2222-2222-2222-222222222222"),
				},
				StreamChunk:     &message.StreamChunk{SequenceNumber: 1, DataPointGroups: benchDataPointGroups()},
				ExtensionFields: &message.DownstreamChunkExtensionFields{},
			},
		},
	}
}

func Benchmark_encoder_EncodeTo(b *testing.B) {
	for _, bm := range benchMessages() {
		b.Run(bm.name, func(b *testing.B) {
			testee := NewEncoding()
			var buf bytes.Buffer
			b.ReportAllocs()
			for b.Loop() {
				buf.Reset()
				if _, err := testee.EncodeTo(&buf, bm.msg); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func Benchmark_encoder_DecodeFrom(b *testing.B) {
	for _, bm := range benchMessages() {
		b.Run(bm.name, func(b *testing.B) {
			testee := NewEncoding()
			var buf bytes.Buffer
			if _, err := testee.EncodeTo(&buf, bm.msg); err != nil {
				b.Fatal(err)
			}
			encoded := buf.Bytes()
			b.ReportAllocs()
			for b.Loop() {
				if _, _, err := testee.DecodeFrom(bytes.NewReader(encoded)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
