package convert_test

import (
	"testing"

	autogen "github.com/aptpod/iscp-go/encoding/autogen"
	. "github.com/aptpod/iscp-go/encoding/convert"
	"github.com/aptpod/iscp-go/errors"
	"github.com/aptpod/iscp-go/message"
	uuid "github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProtoToWire(t *testing.T) {
	tests := []struct {
		name string
		in   *autogen.Message
		want message.Message
	}{
		{name: "pong", in: pongPB, want: pong},
		{name: "ping", in: pingPB, want: ping},
		{name: "connectRequest", in: connectRequestPB, want: connectRequest},
		{name: "connectResponse", in: connectResponsePB, want: connectResponse},
		{name: "disconnect", in: disconnectPB, want: disconnect},
		{name: "downstreamOpenRequest", in: downstreamOpenRequestPB, want: downstreamOpenRequest},
		{name: "downstreamOpenResponse", in: downstreamOpenResponsePB, want: downstreamOpenResponse},
		{name: "downstreamResumeRequest", in: downstreamResumeRequestPB, want: downstreamResumeRequest},
		{name: "downstreamResumeResponse", in: downstreamResumeResponsePB, want: downstreamResumeResponse},
		{name: "downstreamCloseRequest", in: downstreamCloseRequestPB, want: downstreamCloseRequest},
		{name: "downstreamCloseResponse", in: downstreamCloseResponsePB, want: downstreamCloseResponse},
		{name: "upstreamCall", in: upstreamCallPB, want: upstreamCall},
		{name: "upstreamCallAck", in: upstreamCallAckPB, want: upstreamCallAck},
		{name: "downstreamCall", in: downstreamCallPB, want: downstreamCall},
		{name: "upstreamOpenRequest", in: upstreamOpenRequestPB, want: upstreamOpenRequest},
		{name: "upstreamOpenResponse", in: upstreamOpenResponsePB, want: upstreamOpenResponse},
		{name: "upstreamResumeRequest", in: upstreamResumeRequestPB, want: upstreamResumeRequest},
		{name: "upstreamResumeResponse", in: upstreamResumeResponsePB, want: upstreamResumeResponse},
		{name: "upstreamCloseRequest", in: upstreamCloseRequestPB, want: upstreamCloseRequest},
		{name: "upstreamCloseResponse", in: upstreamCloseResponsePB, want: upstreamCloseResponse},
		{name: "upstreamMetadata", in: upstreamMetadataPB, want: upstreamMetadata},
		{name: "upstreamMetadataAck", in: upstreamMetadataAckPB, want: upstreamMetadataAck},
		{name: "downstreamMetadata", in: downstreamMetadataPB, want: downstreamMetadata},
		{name: "downstreamMetadataAck", in: downstreamMetadataAckPB, want: downstreamMetadataAck},
		{name: "upstreamDataPoints", in: upstreamDataPointsPB, want: upstreamDataPoints},
		{name: "upstreamDataPointsAck", in: upstreamDataPointsAckPB, want: upstreamDataPointsAck},
		{name: "downstreamDataPoints", in: downstreamDataPointsPB, want: downstreamDataPoints},
		{name: "downstreamDataPointsAck", in: downstreamDataPointsAckPB, want: downstreamDataPointsAck},
		{name: "downstreamDataPointsAckComplete", in: downstreamDataPointsAckCompletePB, want: downstreamDataPointsAckComplete},

		// extension is nil
		{name: "pongNilExtension", in: pongPBNilExtension, want: pongNilExtension},
		{name: "pingNilExtension", in: pingPBNilExtension, want: pingNilExtension},
		{name: "connectRequestNilExtension", in: connectRequestPBNilExtension, want: connectRequestNilExtension},
		{name: "connectResponseNilExtension", in: connectResponsePBNilExtension, want: connectResponseNilExtension},
		{name: "disconnectNilExtension", in: disconnectPBNilExtension, want: disconnectNilExtension},
		{name: "downstreamOpenRequestNilExtension", in: downstreamOpenRequestPBNilExtension, want: downstreamOpenRequestNilExtension},
		{name: "downstreamOpenResponseNilExtension", in: downstreamOpenResponsePBNilExtension, want: downstreamOpenResponseNilExtension},
		{name: "downstreamResumeRequestNilExtension", in: downstreamResumeRequestPBNilExtension, want: downstreamResumeRequestNilExtension},
		{name: "downstreamResumeResponseNilExtension", in: downstreamResumeResponsePBNilExtension, want: downstreamResumeResponseNilExtension},
		{name: "downstreamCloseRequestNilExtension", in: downstreamCloseRequestPBNilExtension, want: downstreamCloseRequestNilExtension},
		{name: "downstreamCloseResponseNilExtension", in: downstreamCloseResponsePBNilExtension, want: downstreamCloseResponseNilExtension},
		{name: "upstreamCallNilExtension", in: upstreamCallPBNilExtension, want: upstreamCallNilExtension},
		{name: "upstreamCallAckNilExtension", in: upstreamCallAckPBNilExtension, want: upstreamCallAckNilExtension},
		{name: "downstreamCallNilExtension", in: downstreamCallPBNilExtension, want: downstreamCallNilExtension},
		{name: "upstreamOpenRequestNilExtension", in: upstreamOpenRequestPBNilExtension, want: upstreamOpenRequestNilExtension},
		{name: "upstreamOpenResponseNilExtension", in: upstreamOpenResponsePBNilExtension, want: upstreamOpenResponseNilExtension},
		{name: "upstreamResumeRequestNilExtension", in: upstreamResumeRequestPBNilExtension, want: upstreamResumeRequestNilExtension},
		{name: "upstreamResumeResponseNilExtension", in: upstreamResumeResponsePBNilExtension, want: upstreamResumeResponseNilExtension},
		{name: "upstreamCloseRequestNilExtension", in: upstreamCloseRequestPBNilExtension, want: upstreamCloseRequestNilExtension},
		{name: "upstreamCloseResponseNilExtension", in: upstreamCloseResponsePBNilExtension, want: upstreamCloseResponseNilExtension},
		{name: "upstreamMetadataNilExtension", in: upstreamMetadataPBNilExtension, want: upstreamMetadataNilExtension},
		{name: "upstreamMetadataAckNilExtension", in: upstreamMetadataAckPBNilExtension, want: upstreamMetadataAckNilExtension},
		{name: "downstreamMetadataNilExtension", in: downstreamMetadataPBNilExtension, want: downstreamMetadataNilExtension},
		{name: "downstreamMetadataAckNilExtension", in: downstreamMetadataAckPBNilExtension, want: downstreamMetadataAckNilExtension},
		{name: "upstreamDataPointsNilExtension", in: upstreamDataPointsPBNilExtension, want: upstreamDataPointsNilExtension},
		{name: "upstreamDataPointsAckNilExtension", in: upstreamDataPointsAckPBNilExtension, want: upstreamDataPointsAckNilExtension},
		{name: "downstreamDataPointsNilExtension", in: downstreamDataPointsPBNilExtension, want: downstreamDataPointsNilExtension},
		{name: "downstreamDataPointsAckNilExtension", in: downstreamDataPointsAckPBNilExtension, want: downstreamDataPointsAckNilExtension},
		{name: "downstreamDataPointsAckCompleteNilExtension", in: downstreamDataPointsAckCompletePBNilExtension, want: downstreamDataPointsAckCompleteNilExtension},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.NotPanics(t, func() {
				v, err := ProtoToWire(tt.in)
				require.NoError(t, err)
				assert.Equal(t, tt.want, v)
			})
		})
	}
}

func TestProtoToWire_Invalid(t *testing.T) {
	tests := []struct {
		name string
		in   *autogen.Message
	}{
		{name: "connectResponsePBInvalid", in: connectResponsePBInvalid},
		{name: "disconnectPBInvalid ", in: disconnectPBInvalid},
		{name: "upstreamOpenRequestPBInvalidQoS", in: upstreamOpenRequestPBInvalidQoS},
		{name: "upstreamOpenResponsePBInvalidUUID ", in: upstreamOpenResponsePBInvalidUUID},
		{name: "upstreamOpenResponsePBInvalidResultCode ", in: upstreamOpenResponsePBInvalidResultCode},
		{name: "upstreamResumeRequestPBInvalidUUID ", in: upstreamResumeRequestPBInvalidUUID},
		{name: "upstreamResumeResponsePBInvalidResultCode ", in: upstreamResumeResponsePBInvalidResultCode},
		{name: "upstreamCloseRequestPBInvalidUUID ", in: upstreamCloseRequestPBInvalidUUID},
		{name: "upstreamCloseResponsePBInvalidResultCode ", in: upstreamCloseResponsePBInvalidResultCode},
		{name: "downstreamOpenRequestPBInvalidQoS", in: downstreamOpenRequestPBInvalidQoS},
		{name: "downstreamOpenResponsePBInvalidUUID ", in: downstreamOpenResponsePBInvalidUUID},
		{name: "downstreamOpenResponsePBInvalidResultCode ", in: downstreamOpenResponsePBInvalidResultCode},
		{name: "downstreamResumeRequestPBInvalidUUID ", in: downstreamResumeRequestPBInvalidUUID},
		{name: "downstreamResumeResponsePBInvalidResultCode ", in: downstreamResumeResponsePBInvalidResultCode},
		{name: "downstreamCloseRequestPBInvalidUUID ", in: downstreamCloseRequestPBInvalidUUID},
		{name: "downstreamCloseResponsePBInvalidResultCode ", in: downstreamCloseResponsePBInvalidResultCode},
		{name: "upstreamDataPointsAckPBInvalidResultCode ", in: upstreamDataPointsAckPBInvalidResultCode},
		{name: "upstreamDataPointsAckPBInvalidResultCode ", in: upstreamDataPointsAckPBInvalidResultCode},
		{name: "downstreamDataPointsPBInvalidUUID ", in: downstreamDataPointsPBInvalidUUID},
		{name: "downstreamDataPointsAckPBInvalidResultCode ", in: downstreamDataPointsAckPBInvalidResultCode},
		{name: "upstreamMetadataAckPBInvalidResultCode ", in: upstreamMetadataAckPBInvalidResultCode},
		{name: "downstreamMetadataAckPBInvalidResultCode ", in: downstreamMetadataAckPBInvalidResultCode},
		{name: "downstreamMetadataPBInvalidUpstreamOpenUUID", in: downstreamMetadataPBInvalidUpstreamOpenUUID},
		{name: "downstreamMetadataPBInvalidUpstreamQoS", in: downstreamMetadataPBInvalidUpstreamQoS},
		{name: "downstreamMetadataPBInvalidUpstreamResume", in: downstreamMetadataPBInvalidUpstreamResume},
		{name: "downstreamMetadataPBInvalidDownstreamOpenQoS", in: downstreamMetadataPBInvalidDownstreamOpenQoS},
		{name: "downstreamMetadataPBInvalidDownstreamOpenUUID", in: downstreamMetadataPBInvalidDownstreamOpenUUID},
		{name: "downstreamMetadataPBInvalidDownstreamResume", in: downstreamMetadataPBInvalidDownstreamResume},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := ProtoToWire(tt.in)
			assert.ErrorIs(t, err, errors.ErrMalformedMessage)
			assert.Nil(t, v)
		})
	}
}

func Test_toResultCode(t *testing.T) {
	tests := []struct {
		name string
		in   autogen.ResultCode
		want message.ResultCode
	}{
		{name: "SUCCEEDED", in: autogen.ResultCode_SUCCEEDED, want: message.ResultCodeSucceeded},
		{name: "NORMAL_CLOSURE", in: autogen.ResultCode_NORMAL_CLOSURE, want: message.ResultCodeSucceeded},
		{name: "INCOMPATIBLE_VERSION", in: autogen.ResultCode_INCOMPATIBLE_VERSION, want: message.ResultCodeIncompatibleVersion},
		{name: "MAXIMUM_DATA_ID_ALIAS", in: autogen.ResultCode_MAXIMUM_DATA_ID_ALIAS, want: message.ResultCodeMaximumDataIDAlias},
		{name: "MAXIMUM_UPSTREAM_ALIAS", in: autogen.ResultCode_MAXIMUM_UPSTREAM_ALIAS, want: message.ResultCodeMaximumUpstreamAlias},
		{name: "UNSPECIFIED_ERROR", in: autogen.ResultCode_UNSPECIFIED_ERROR, want: message.ResultCodeUnspecifiedError},
		{name: "NO_NODE_ID", in: autogen.ResultCode_NO_NODE_ID, want: message.ResultCodeNoNodeID},
		{name: "AUTH_FAILED", in: autogen.ResultCode_AUTH_FAILED, want: message.ResultCodeAuthFailed},
		{name: "CONNECT_TIMEOUT", in: autogen.ResultCode_CONNECT_TIMEOUT, want: message.ResultCodeConnectTimeout},
		{name: "MALFORMED_MESSAGE", in: autogen.ResultCode_MALFORMED_MESSAGE, want: message.ResultCodeMalformedMessage},
		{name: "PROTOCOL_ERROR", in: autogen.ResultCode_PROTOCOL_ERROR, want: message.ResultCodeProtocolError},
		{name: "ACK_TIMEOUT", in: autogen.ResultCode_ACK_TIMEOUT, want: message.ResultCodeAckTimeout},
		{name: "INVALID_PAYLOAD", in: autogen.ResultCode_INVALID_PAYLOAD, want: message.ResultCodeInvalidPayload},
		{name: "INVALID_DATA_ID", in: autogen.ResultCode_INVALID_DATA_ID, want: message.ResultCodeInvalidDataID},
		{name: "INVALID_DATA_ID_ALIAS", in: autogen.ResultCode_INVALID_DATA_ID_ALIAS, want: message.ResultCodeInvalidDataIDAlias},
		{name: "INVALID_DATA_FILTER", in: autogen.ResultCode_INVALID_DATA_FILTER, want: message.ResultCodeInvalidDataFilter},
		{name: "STREAM_NOT_FOUND", in: autogen.ResultCode_STREAM_NOT_FOUND, want: message.ResultCodeStreamNotFound},
		{name: "RESUME_REQUEST_CONFLICT", in: autogen.ResultCode_RESUME_REQUEST_CONFLICT, want: message.ResultCodeResumeRequestConflict},
		{name: "PROCESS_FAILED", in: autogen.ResultCode_PROCESS_FAILED, want: message.ResultCodeProcessFailed},
		{name: "DESIRED_QOS_NOT_SUPPORTED", in: autogen.ResultCode_DESIRED_QOS_NOT_SUPPORTED, want: message.ResultCodeDesiredQosNotSupported},
		{name: "PING_TIMEOUT", in: autogen.ResultCode_PING_TIMEOUT, want: message.ResultCodePingTimeout},
		{name: "TOO_LARGE_MESSAGE_SIZE", in: autogen.ResultCode_TOO_LARGE_MESSAGE_SIZE, want: message.ResultCodeTooLargeMessageSize},
		{name: "TOO_MANY_DATA_ID_ALIASES", in: autogen.ResultCode_TOO_MANY_DATA_ID_ALIASES, want: message.ResultCodeTooManyDataIDAliases},
		{name: "TOO_MANY_STREAMS", in: autogen.ResultCode_TOO_MANY_STREAMS, want: message.ResultCodeTooManyStreams},
		{name: "TOO_LONG_ACK_INTERVAL", in: autogen.ResultCode_TOO_LONG_ACK_INTERVAL, want: message.ResultCodeTooLongAckInterval},
		{name: "TOO_MANY_DOWNSTREAM_FILTERS", in: autogen.ResultCode_TOO_MANY_DOWNSTREAM_FILTERS, want: message.ResultCodeTooManyDownstreamFilters},
		{name: "TOO_MANY_DATA_FILTERS", in: autogen.ResultCode_TOO_MANY_DATA_FILTERS, want: message.ResultCodeTooManyDataFilters},
		{name: "TOO_LONG_EXPIRY_INTERVAL", in: autogen.ResultCode_TOO_LONG_EXPIRY_INTERVAL, want: message.ResultCodeTooLongExpiryInterval},
		{name: "TOO_LONG_PING_TIMEOUT", in: autogen.ResultCode_TOO_LONG_PING_TIMEOUT, want: message.ResultCodeTooLongPingTimeout},
		{name: "TOO_SHORT_PING_TIMEOUT", in: autogen.ResultCode_TOO_SHORT_PING_TIMEOUT, want: message.ResultCodeTooShortPingTimeout},
		{name: "NODE_ID_MISMATCH", in: autogen.ResultCode_NODE_ID_MISMATCH, want: message.ResultCodeNodeIDMismatch},
		{name: "RATE_LIMIT_REACHED", in: autogen.ResultCode_RATE_LIMIT_REACHED, want: message.ResultCodeRateLimitReached},
		{name: "SESSION_NOT_FOUND", in: autogen.ResultCode_SESSION_NOT_FOUND, want: message.ResultCodeSessionNotFound},
		{name: "SESSION_ALREADY_CLOSED", in: autogen.ResultCode_SESSION_ALREADY_CLOSED, want: message.ResultCodeSessionAlreadyClosed},
		{name: "SESSION_CANNOT_CLOSED", in: autogen.ResultCode_SESSION_CANNOT_CLOSED, want: message.ResultCodeSessionCannotClosed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ToResultCode(tt.in)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_toQoS(t *testing.T) {
	tests := []struct {
		name string
		in   autogen.QoS
		want message.QoS
	}{
		{name: "RELIABLE", in: autogen.QoS_RELIABLE, want: message.QoSReliable},
		{name: "UNRELIABLE", in: autogen.QoS_UNRELIABLE, want: message.QoSUnreliable},
		{name: "PARTIAL", in: autogen.QoS_PARTIAL, want: message.QoSPartial},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ToQoS(tt.in)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_toUpstreamDataIDOrAlias(t *testing.T) {
	type args struct{}
	tests := []struct {
		name string
		in   *autogen.DataPointGroup
		want message.DataIDOrAlias
	}{
		{name: "DataId", in: &autogen.DataPointGroup{
			DataIdOrAlias: &autogen.DataPointGroup_DataId{DataId: &autogen.DataID{Name: "name", Type: "type"}},
			DataPoints:    []*autogen.DataPoint{},
		}, want: &message.DataID{Name: "name", Type: "type"}},
		{name: "DataIdAlias", in: &autogen.DataPointGroup{
			DataIdOrAlias: &autogen.DataPointGroup_DataIdAlias{DataIdAlias: 1},
		}, want: message.DataIDAlias(1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.NotPanics(t, func() {
				got, err := ToDataIDOrAlias(tt.in.DataIdOrAlias)
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			})
		})
	}
}

func Test_toDownstreamDataIDOrAlias(t *testing.T) {
	type args struct{}
	tests := []struct {
		name string
		in   *autogen.DataPointGroup
		want message.DataIDOrAlias
	}{
		{name: "DataId", in: &autogen.DataPointGroup{
			DataIdOrAlias: &autogen.DataPointGroup_DataId{DataId: &autogen.DataID{Name: "name", Type: "type"}},
			DataPoints:    []*autogen.DataPoint{},
		}, want: &message.DataID{Name: "name", Type: "type"}},
		{name: "DataIdAlias", in: &autogen.DataPointGroup{
			DataIdOrAlias: &autogen.DataPointGroup_DataIdAlias{DataIdAlias: 1},
		}, want: message.DataIDAlias(1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.NotPanics(t, func() {
				got, err := ToDataIDOrAlias(tt.in.DataIdOrAlias)
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			})
		})
	}
}

func Test_toDownstreamMetadata(t *testing.T) {
	tests := []struct {
		name string
		in   *autogen.DownstreamMetadata
		want message.Metadata
	}{
		{name: "BaseTime", in: downstreamMetadataBaseTimePB, want: metadataBaseTime},
		{name: "UpstreamOpen", in: downstreamMetadataUpstreamOpenPB, want: metadataUpstreamOpen},
		{name: "UpstreamAbnormal", in: downstreamMetadataUpstreamAbnormalClosePB, want: metadataUpstreamAbnormalClose},
		{name: "UpstreamResume", in: downstreamMetadataUpstreamResumePB, want: metadataUpstreamResume},
		{name: "UpstreamNormal", in: downstreamMetadataUpstreamNormalClosePB, want: metadataUpstreamNormalClose},
		{name: "DownstreamOpen", in: downstreamMetadataDownstreamOpenPB, want: metadataDownstreamOpen},
		{name: "DownstreamAbnormal", in: downstreamMetadataDownstreamAbnormalClosePB, want: metadataDownstreamAbnormalClose},
		{name: "DownstreamResume", in: downstreamMetadataDownstreamResumePB, want: metadataDownstreamResume},
		{name: "DownstreamNormal", in: downstreamMetadataDownstreamNormalClosePB, want: metadataDownstreamNormalClose},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ToDownstreamMetadata(tt.in)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_toUpstreamMetadata(t *testing.T) {
	tests := []struct {
		name string
		in   *autogen.UpstreamMetadata
		want message.Metadata
	}{
		{name: "BaseTime", in: upstreamMetadataBaseTimePB, want: metadataBaseTime},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ToUpstreamMetadata(tt.in)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_toUpstreamOrAlias(t *testing.T) {
	type args struct{}
	tests := []struct {
		name string
		args args
		in   *autogen.DownstreamChunk
		want message.UpstreamOrAlias
	}{
		{name: "UpstreamAlias", in: &autogen.DownstreamChunk{UpstreamOrAlias: &autogen.DownstreamChunk_UpstreamAlias{UpstreamAlias: 1}}, want: message.UpstreamAlias(1)},
		{name: "Upstream", in: &autogen.DownstreamChunk{UpstreamOrAlias: &autogen.DownstreamChunk_UpstreamInfo{UpstreamInfo: &autogen.UpstreamInfo{
			SourceNodeId: "22222222-2222-2222-2222-222222222222",
			SessionId:    "SessionId",
			StreamId:     mustMarshalBinary(uuid.MustParse("11111111-1111-1111-1111-111111111111")),
		}}}, want: &message.UpstreamInfo{
			SourceNodeID: "22222222-2222-2222-2222-222222222222",
			SessionID:    "SessionId",
			StreamID:     uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ToUpstreamOrAlias(tt.in)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
