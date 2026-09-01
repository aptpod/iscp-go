package protobuf_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/aptpod/iscp-go/encoding"
	. "github.com/aptpod/iscp-go/encoding/protobuf"
	"github.com/aptpod/iscp-go/message"
)

func Test_encoder_ContentType(t *testing.T) {
	assert.Equal(t, encoding.ContentTypeBinary, NewEncoding().ContentType())
}

func Test_encoder_EncodeTo_DecodeFrom(t *testing.T) {
	tests := []struct {
		name string
		in   message.Message
		exp  message.Message
	}{
		{name: "ping ", in: ping, exp: ping},
		{name: "pong ", in: pong, exp: pong},
		{name: "connectRequest ", in: connectRequest, exp: connectRequest},
		{name: "connectResponse ", in: connectResponse, exp: connectResponse},
		{name: "disconnect ", in: disconnect, exp: disconnect},
		{name: "downstreamOpenRequest ", in: downstreamOpenRequest, exp: downstreamOpenRequest},
		{name: "downstreamOpenResponse ", in: downstreamOpenResponse, exp: downstreamOpenResponse},
		{name: "downstreamResumeRequest ", in: downstreamResumeRequest, exp: downstreamResumeRequest},
		{name: "downstreamResumeResponse ", in: downstreamResumeResponse, exp: downstreamResumeResponse},
		{name: "downstreamCloseRequest ", in: downstreamCloseRequest, exp: downstreamCloseRequest},
		{name: "downstreamCloseResponse ", in: downstreamCloseResponse, exp: downstreamCloseResponse},
		{name: "upstreamCall ", in: upstreamCall, exp: upstreamCall},
		{name: "downstreamCall ", in: downstreamCall, exp: downstreamCall},
		{name: "upstreamMetadata ", in: upstreamMetadata, exp: upstreamMetadata},
		{name: "upstreamMetadataAck ", in: upstreamMetadataAck, exp: upstreamMetadataAck},
		{name: "downstreamMetadata ", in: downstreamMetadata, exp: downstreamMetadata},
		{name: "downstreamMetadataAck ", in: downstreamMetadataAck, exp: downstreamMetadataAck},
		{name: "upstreamOpenRequest ", in: upstreamOpenRequest, exp: upstreamOpenRequest},
		{name: "upstreamOpenResponse ", in: upstreamOpenResponse, exp: upstreamOpenResponse},
		{name: "upstreamResumeRequest ", in: upstreamResumeRequest, exp: upstreamResumeRequest},
		{name: "upstreamResumeResponse ", in: upstreamResumeResponse, exp: upstreamResumeResponse},
		{name: "upstreamCloseRequest ", in: upstreamCloseRequest, exp: upstreamCloseRequest},
		{name: "upstreamCloseResponse ", in: upstreamCloseResponse, exp: upstreamCloseResponse},
		{name: "upstreamDataPoints ", in: upstreamDataPoints, exp: upstreamDataPoints},
		{name: "upstreamDataPointsAck ", in: upstreamDataPointsAck, exp: upstreamDataPointsAck},
		{name: "downstreamDataPoints ", in: downstreamDataPoints, exp: downstreamDataPoints},
		{name: "downstreamDataPointsAck ", in: downstreamDataPointsAck, exp: downstreamDataPointsAck},
		{name: "downstreamDataPointsAckComplete", in: downstreamDataPointsAckComplete, exp: downstreamDataPointsAckComplete},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			testee := NewEncoding()
			// execute
			writtenBytes, err := testee.EncodeTo(&buf, tt.in)
			assert.NoError(t, err)

			readBytes, m, err := testee.DecodeFrom(&buf)
			assert.NoError(t, err)

			// verification
			assert.Equal(t, readBytes, writtenBytes)
			assert.Equal(t, tt.exp, m)
		})
	}
}

// errPanicSentinel は、 recover() 時に型情報が保持されることを検証するための型付きエラーです。
var errPanicSentinel = errors.New("panic sentinel for recover test")

// panicWriter は、 Write 呼び出し時に errPanicSentinel を panic させる io.Writer です。
type panicWriter struct{}

func (panicWriter) Write(p []byte) (int, error) {
	panic(errPanicSentinel)
}

// panicReader は、 Read 呼び出し時に errPanicSentinel を panic させる io.Reader です。
type panicReader struct{}

func (panicReader) Read(p []byte) (int, error) {
	panic(errPanicSentinel)
}

func Test_encoder_EncodeTo_RecoversTypedError(t *testing.T) {
	testee := NewEncoding()
	_, err := testee.EncodeTo(panicWriter{}, ping)
	assert.ErrorIs(t, err, errPanicSentinel)
}

func Test_encoder_DecodeFrom_RecoversTypedError(t *testing.T) {
	testee := NewEncoding()
	_, _, err := testee.DecodeFrom(panicReader{})
	assert.ErrorIs(t, err, errPanicSentinel)
}
