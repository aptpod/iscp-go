package convert_test

import (
	"testing"

	autogen "github.com/aptpod/iscp-proto/gen/gogofast/iscp2/v1"
	autogenextensions "github.com/aptpod/iscp-proto/gen/gogofast/iscp2/v1/extensions"
	uuid "github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	. "github.com/aptpod/iscp-go/encoding/convert"
	"github.com/aptpod/iscp-go/errors"
	"github.com/aptpod/iscp-go/message"
)

func TestWireToProto(t *testing.T) {
	tests := []struct {
		name string
		in   message.Message
		want *autogen.Message
	}{
		{name: "pong", want: pongPB, in: pong},
		{name: "ping", want: pingPB, in: ping},
		{name: "connectRequest", want: connectRequestPB, in: connectRequest},
		{name: "connectResponse", want: connectResponsePB, in: connectResponse},
		{name: "disconnect", want: disconnectPB, in: disconnect},
		{name: "downstreamOpenRequest", want: downstreamOpenRequestPB, in: downstreamOpenRequest},
		{name: "downstreamOpenResponse", want: downstreamOpenResponsePB, in: downstreamOpenResponse},
		{name: "downstreamOpenResponse_server_time_zero", want: downstreamOpenResponseServerTimeZeroPB, in: downstreamOpenResponseServerTimeZero},
		{name: "downstreamResumeRequest", want: downstreamResumeRequestPB, in: downstreamResumeRequest},
		{name: "downstreamResumeResponse", want: downstreamResumeResponsePB, in: downstreamResumeResponse},
		{name: "downstreamCloseRequest", want: downstreamCloseRequestPB, in: downstreamCloseRequest},
		{name: "downstreamCloseResponse", want: downstreamCloseResponsePB, in: downstreamCloseResponse},
		{name: "upstreamCall", want: upstreamCallPB, in: upstreamCall},
		{name: "upstreamCallAck", want: upstreamCallAckPB, in: upstreamCallAck},
		{name: "downstreamCall", want: downstreamCallPB, in: downstreamCall},
		{name: "upstreamOpenRequest", want: upstreamOpenRequestPB, in: upstreamOpenRequest},
		{name: "upstreamOpenResponse", want: upstreamOpenResponsePB, in: upstreamOpenResponse},
		{name: "upstreamOpenResponse_server_time_zero", want: upstreamOpenResponseServerTimeZeroPB, in: upstreamOpenResponseServerTimeZero},
		{name: "upstreamResumeRequest", want: upstreamResumeRequestPB, in: upstreamResumeRequest},
		{name: "upstreamResumeResponse", want: upstreamResumeResponsePB, in: upstreamResumeResponse},
		{name: "upstreamCloseRequest", want: upstreamCloseRequestPB, in: upstreamCloseRequest},
		{name: "upstreamCloseResponse", want: upstreamCloseResponsePB, in: upstreamCloseResponse},
		{name: "upstreamMetadata", want: upstreamMetadataPB, in: upstreamMetadata},
		{name: "upstreamMetadataAck", want: upstreamMetadataAckPB, in: upstreamMetadataAck},
		{name: "downstreamMetadata", want: downstreamMetadataPB, in: downstreamMetadata},
		{name: "downstreamMetadataAck", want: downstreamMetadataAckPB, in: downstreamMetadataAck},
		{name: "upstreamDataPoints", want: upstreamDataPointsPB, in: upstreamDataPoints},
		{name: "upstreamDataPointsAck", want: upstreamDataPointsAckPB, in: upstreamDataPointsAck},
		{name: "downstreamDataPoints", want: downstreamDataPointsPB, in: downstreamDataPoints},
		{name: "downstreamDataPointsAck", want: downstreamDataPointsAckPB, in: downstreamDataPointsAck},
		{name: "downstreamDataPointsAckComplete", want: downstreamDataPointsAckCompletePB, in: downstreamDataPointsAckComplete},

		// extension is nil
		{name: "pongNilExtension", want: pongPBNilExtension, in: pongNilExtension},
		{name: "pingNilExtension", want: pingPBNilExtension, in: pingNilExtension},
		{name: "connectRequestNilExtension", want: connectRequestPBNilExtension, in: connectRequestNilExtension},
		{name: "connectResponseNilExtension", want: connectResponsePBNilExtension, in: connectResponseNilExtension},
		{name: "disconnectNilExtension", want: disconnectPBNilExtension, in: disconnectNilExtension},
		{name: "downstreamOpenRequestNilExtension", want: downstreamOpenRequestPBNilExtension, in: downstreamOpenRequestNilExtension},
		{name: "downstreamOpenResponseNilExtension", want: downstreamOpenResponsePBNilExtension, in: downstreamOpenResponseNilExtension},
		{name: "downstreamResumeRequestNilExtension", want: downstreamResumeRequestPBNilExtension, in: downstreamResumeRequestNilExtension},
		{name: "downstreamResumeResponseNilExtension", want: downstreamResumeResponsePBNilExtension, in: downstreamResumeResponseNilExtension},
		{name: "downstreamCloseRequestNilExtension", want: downstreamCloseRequestPBNilExtension, in: downstreamCloseRequestNilExtension},
		{name: "downstreamCloseResponseNilExtension", want: downstreamCloseResponsePBNilExtension, in: downstreamCloseResponseNilExtension},
		{name: "upstreamCallNilExtension", want: upstreamCallPBNilExtension, in: upstreamCallNilExtension},
		{name: "upstreamCallAckNilExtension", want: upstreamCallAckPBNilExtension, in: upstreamCallAckNilExtension},
		{name: "downstreamCallNilExtension", want: downstreamCallPBNilExtension, in: downstreamCallNilExtension},
		{name: "upstreamOpenRequestNilExtension", want: upstreamOpenRequestPBNilExtension, in: upstreamOpenRequestNilExtension},
		{name: "upstreamOpenResponseNilExtension", want: upstreamOpenResponsePBNilExtension, in: upstreamOpenResponseNilExtension},
		{name: "upstreamResumeRequestNilExtension", want: upstreamResumeRequestPBNilExtension, in: upstreamResumeRequestNilExtension},
		{name: "upstreamResumeResponseNilExtension", want: upstreamResumeResponsePBNilExtension, in: upstreamResumeResponseNilExtension},
		{name: "upstreamCloseRequestNilExtension", want: upstreamCloseRequestPBNilExtension, in: upstreamCloseRequestNilExtension},
		{name: "upstreamCloseResponseNilExtension", want: upstreamCloseResponsePBNilExtension, in: upstreamCloseResponseNilExtension},
		{name: "upstreamMetadataNilExtension", want: upstreamMetadataPBNilExtension, in: upstreamMetadataNilExtension},
		{name: "upstreamMetadataAckNilExtension", want: upstreamMetadataAckPBNilExtension, in: upstreamMetadataAckNilExtension},
		{name: "downstreamMetadataNilExtension", want: downstreamMetadataPBNilExtension, in: downstreamMetadataNilExtension},
		{name: "downstreamMetadataAckNilExtension", want: downstreamMetadataAckPBNilExtension, in: downstreamMetadataAckNilExtension},
		{name: "upstreamDataPointsNilExtension", want: upstreamDataPointsPBNilExtension, in: upstreamDataPointsNilExtension},
		{name: "upstreamDataPointsAckNilExtension", want: upstreamDataPointsAckPBNilExtension, in: upstreamDataPointsAckNilExtension},
		{name: "downstreamDataPointsNilExtension", want: downstreamDataPointsPBNilExtension, in: downstreamDataPointsNilExtension},
		{name: "downstreamDataPointsAckNilExtension", want: downstreamDataPointsAckPBNilExtension, in: downstreamDataPointsAckNilExtension},
		{name: "downstreamDataPointsAckCompleteNilExtension", want: downstreamDataPointsAckCompletePBNilExtension, in: downstreamDataPointsAckCompleteNilExtension},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.NotPanics(t, func() {
				got, err := WireToProto(tt.in)
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			})
		})
	}
}

func Test_toResultCodeProto(t *testing.T) {
	tests := []struct {
		name string
		in   message.ResultCode
		want autogen.ResultCode
	}{
		{name: "SUCCEEDED", want: autogen.ResultCode_SUCCEEDED, in: message.ResultCodeSucceeded},
		{name: "NORMAL_CLOSURE", want: autogen.ResultCode_NORMAL_CLOSURE, in: message.ResultCodeNormalClosure},
		{name: "INCOMPATIBLE_VERSION", want: autogen.ResultCode_INCOMPATIBLE_VERSION, in: message.ResultCodeIncompatibleVersion},
		{name: "MAXIMUM_DATA_ID_ALIAS", want: autogen.ResultCode_MAXIMUM_DATA_ID_ALIAS, in: message.ResultCodeMaximumDataIDAlias},
		{name: "MAXIMUM_UPSTREAM_ALIAS", want: autogen.ResultCode_MAXIMUM_UPSTREAM_ALIAS, in: message.ResultCodeMaximumUpstreamAlias},
		{name: "UNSPECIFIED_ERROR", want: autogen.ResultCode_UNSPECIFIED_ERROR, in: message.ResultCodeUnspecifiedError},
		{name: "NO_NODE_ID", want: autogen.ResultCode_NO_NODE_ID, in: message.ResultCodeNoNodeID},
		{name: "AUTH_FAILED", want: autogen.ResultCode_AUTH_FAILED, in: message.ResultCodeAuthFailed},
		{name: "CONNECT_TIMEOUT", want: autogen.ResultCode_CONNECT_TIMEOUT, in: message.ResultCodeConnectTimeout},
		{name: "MALFORMED_MESSAGE", want: autogen.ResultCode_MALFORMED_MESSAGE, in: message.ResultCodeMalformedMessage},
		{name: "PROTOCOL_ERROR", want: autogen.ResultCode_PROTOCOL_ERROR, in: message.ResultCodeProtocolError},
		{name: "ACK_TIMEOUT", want: autogen.ResultCode_ACK_TIMEOUT, in: message.ResultCodeAckTimeout},
		{name: "INVALID_PAYLOAD", want: autogen.ResultCode_INVALID_PAYLOAD, in: message.ResultCodeInvalidPayload},
		{name: "INVALID_DATA_ID", want: autogen.ResultCode_INVALID_DATA_ID, in: message.ResultCodeInvalidDataID},
		{name: "INVALID_DATA_ID_ALIAS", want: autogen.ResultCode_INVALID_DATA_ID_ALIAS, in: message.ResultCodeInvalidDataIDAlias},
		{name: "INVALID_DATA_FILTER", want: autogen.ResultCode_INVALID_DATA_FILTER, in: message.ResultCodeInvalidDataFilter},
		{name: "STREAM_NOT_FOUND", want: autogen.ResultCode_STREAM_NOT_FOUND, in: message.ResultCodeStreamNotFound},
		{name: "RESUME_REQUEST_CONFLICT", want: autogen.ResultCode_RESUME_REQUEST_CONFLICT, in: message.ResultCodeResumeRequestConflict},
		{name: "PROCESS_FAILED", want: autogen.ResultCode_PROCESS_FAILED, in: message.ResultCodeProcessFailed},
		{name: "DESIRED_QOS_NOT_SUPPORTED", want: autogen.ResultCode_DESIRED_QOS_NOT_SUPPORTED, in: message.ResultCodeDesiredQosNotSupported},
		{name: "PING_TIMEOUT", want: autogen.ResultCode_PING_TIMEOUT, in: message.ResultCodePingTimeout},
		{name: "TOO_LARGE_MESSAGE_SIZE", want: autogen.ResultCode_TOO_LARGE_MESSAGE_SIZE, in: message.ResultCodeTooLargeMessageSize},
		{name: "TOO_MANY_DATA_ID_ALIASES", want: autogen.ResultCode_TOO_MANY_DATA_ID_ALIASES, in: message.ResultCodeTooManyDataIDAliases},
		{name: "TOO_MANY_STREAMS", want: autogen.ResultCode_TOO_MANY_STREAMS, in: message.ResultCodeTooManyStreams},
		{name: "TOO_LONG_ACK_INTERVAL", want: autogen.ResultCode_TOO_LONG_ACK_INTERVAL, in: message.ResultCodeTooLongAckInterval},
		{name: "TOO_MANY_DOWNSTREAM_FILTERS", want: autogen.ResultCode_TOO_MANY_DOWNSTREAM_FILTERS, in: message.ResultCodeTooManyDownstreamFilters},
		{name: "TOO_MANY_DATA_FILTERS", want: autogen.ResultCode_TOO_MANY_DATA_FILTERS, in: message.ResultCodeTooManyDataFilters},
		{name: "TOO_LONG_EXPIRY_INTERVAL", want: autogen.ResultCode_TOO_LONG_EXPIRY_INTERVAL, in: message.ResultCodeTooLongExpiryInterval},
		{name: "TOO_LONG_PING_TIMEOUT", want: autogen.ResultCode_TOO_LONG_PING_TIMEOUT, in: message.ResultCodeTooLongPingTimeout},
		{name: "TOO_SHORT_PING_TIMEOUT", want: autogen.ResultCode_TOO_SHORT_PING_TIMEOUT, in: message.ResultCodeTooShortPingTimeout},
		{name: "NODE_ID_MISMATCH", want: autogen.ResultCode_NODE_ID_MISMATCH, in: message.ResultCodeNodeIDMismatch},
		{name: "RATE_LIMIT_REACHED", want: autogen.ResultCode_RATE_LIMIT_REACHED, in: message.ResultCodeRateLimitReached},
		{name: "SESSION_NOT_FOUND", want: autogen.ResultCode_SESSION_NOT_FOUND, in: message.ResultCodeSessionNotFound},
		{name: "SESSION_ALREADY_CLOSED", want: autogen.ResultCode_SESSION_ALREADY_CLOSED, in: message.ResultCodeSessionAlreadyClosed},
		{name: "SESSION_CANNOT_CLOSED", want: autogen.ResultCode_SESSION_CANNOT_CLOSED, in: message.ResultCodeSessionCannotClosed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ToResultCodeProto(tt.in)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_toQoSProto(t *testing.T) {
	tests := []struct {
		name string
		want autogen.QoS
		in   message.QoS
	}{
		{name: "RELIABLE", want: autogen.QoS_RELIABLE, in: message.QoSReliable},
		{name: "UNRELIABLE", want: autogen.QoS_UNRELIABLE, in: message.QoSUnreliable},
		{name: "PARTIAL", want: autogen.QoS_PARTIAL, in: message.QoSPartial},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ToQoSProto(tt.in)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_toDataPointGroupsProto(t *testing.T) {
	type args struct{}
	tests := []struct {
		name string
		want *autogen.DataPointGroup
		in   message.DataIDOrAlias
	}{
		{
			name: "DataId",
			want: &autogen.DataPointGroup{
				DataIdOrAlias: &autogen.DataPointGroup_DataId{DataId: &autogen.DataID{Name: "name", Type: "type"}},
				DataPoints:    []*autogen.DataPoint{},
			},
			in: &message.DataID{Name: "name", Type: "type"},
		},
		{
			name: "DataIdAlias",
			want: &autogen.DataPointGroup{
				DataIdOrAlias: &autogen.DataPointGroup_DataIdAlias{DataIdAlias: 1},
				DataPoints:    []*autogen.DataPoint{},
			},
			in: message.DataIDAlias(1),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ToDataPointGroupsProto([]*message.DataPointGroup{
				{
					DataIDOrAlias: tt.in,
					DataPoints:    []*message.DataPoint{},
				},
			})
			require.NoError(t, err)
			assert.Equal(t, []*autogen.DataPointGroup{tt.want}, got)
		})
	}
}

func Test_toDownstreamMetadataProto(t *testing.T) {
	tests := []struct {
		name string
		want *autogen.DownstreamMetadata
		in   message.Metadata
	}{
		{name: "BaseTime", want: downstreamMetadataBaseTimePB, in: metadataBaseTime},
		{name: "UpstreamOpen", want: downstreamMetadataUpstreamOpenPB, in: metadataUpstreamOpen},
		{name: "UpstreamAbnormal", want: downstreamMetadataUpstreamAbnormalClosePB, in: metadataUpstreamAbnormalClose},
		{name: "UpstreamResume", want: downstreamMetadataUpstreamResumePB, in: metadataUpstreamResume},
		{name: "UpstreamNormal", want: downstreamMetadataUpstreamNormalClosePB, in: metadataUpstreamNormalClose},
		{name: "DownstreamOpen", want: downstreamMetadataDownstreamOpenPB, in: metadataDownstreamOpen},
		{name: "DownstreamAbnormal", want: downstreamMetadataDownstreamAbnormalClosePB, in: metadataDownstreamAbnormalClose},
		{name: "DownstreamResume", want: downstreamMetadataDownstreamResumePB, in: metadataDownstreamResume},
		{name: "DownstreamNormal", want: downstreamMetadataDownstreamNormalClosePB, in: metadataDownstreamNormalClose},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ToDownstreamMetadataProto(&message.DownstreamMetadata{
				RequestID:       1,
				Metadata:        tt.in,
				SourceNodeID:    "SourceNodeId",
				ExtensionFields: &message.DownstreamMetadataExtensionFields{},
			})
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_toUpstreamMetadataProto(t *testing.T) {
	tests := []struct {
		name string
		want *autogen.UpstreamMetadata
		in   message.SendableMetadata
	}{
		{name: "BaseTime", want: upstreamMetadataBaseTimePB, in: metadataBaseTime},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ToUpstreamMetadataProto(&message.UpstreamMetadata{
				RequestID: 1,
				Metadata:  tt.in,
				ExtensionFields: &message.UpstreamMetadataExtensionFields{
					Persist: true,
				},
			})
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_toUpstreamOrAliasProto(t *testing.T) {
	tests := []struct {
		name string
		want *autogen.DownstreamChunk
		in   message.UpstreamOrAlias
	}{
		{
			name: "UpstreamAlias",
			want: &autogen.DownstreamChunk{
				StreamIdAlias:   0,
				UpstreamOrAlias: &autogen.DownstreamChunk_UpstreamAlias{UpstreamAlias: 1},
				StreamChunk: &autogen.StreamChunk{
					DataPointGroups: []*autogen.DataPointGroup{},
				},
				ExtensionFields: &autogenextensions.DownstreamChunkExtensionFields{},
			},
			in: message.UpstreamAlias(1),
		},
		{
			name: "Upstream",
			want: &autogen.DownstreamChunk{
				StreamIdAlias: 0,
				UpstreamOrAlias: &autogen.DownstreamChunk_UpstreamInfo{
					UpstreamInfo: &autogen.UpstreamInfo{
						SourceNodeId: "SourceNodeId",
						SessionId:    "SessionId",
						StreamId:     mustMarshalBinary(uuid.MustParse("11111111-1111-1111-1111-111111111111")),
					},
				},
				StreamChunk: &autogen.StreamChunk{
					DataPointGroups: []*autogen.DataPointGroup{},
				},
				ExtensionFields: &autogenextensions.DownstreamChunkExtensionFields{},
			}, in: &message.UpstreamInfo{
				SourceNodeID: "SourceNodeId",
				SessionID:    "SessionId",
				StreamID:     uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ToDownstreamChunkProto(&message.DownstreamChunk{
				StreamIDAlias:   0,
				UpstreamOrAlias: tt.in,
				StreamChunk:     &message.StreamChunk{},
				ExtensionFields: &message.DownstreamChunkExtensionFields{},
			})
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestWireToProto_Invalid(t *testing.T) {
	tests := []struct {
		name string
		in   message.Message
	}{
		{name: "connectResponseInvalid", in: connectResponseInvalid},
		{name: "disconnectInvalid", in: disconnectInvalid},
		{name: "upstreamOpenRequestInvalidQoS", in: upstreamOpenRequestInvalidQoS},
		{name: "upstreamOpenResponseInvalidUUID", in: upstreamOpenResponseInvalidUUID},
		{name: "upstreamOpenResponseInvalidResultCode", in: upstreamOpenResponseInvalidResultCode},
		{name: "upstreamResumeResponseInvalidResultCode", in: upstreamResumeResponseInvalidResultCode},
		{name: "upstreamCloseResponseInvalidResultCode", in: upstreamCloseResponseInvalidResultCode},
		{name: "downstreamOpenRequestInvalidQoS", in: downstreamOpenRequestInvalidQoS},
		{name: "downstreamOpenResponseInvalidResultCode", in: downstreamOpenResponseInvalidResultCode},
		{name: "downstreamResumeResponseInvalidResultCode", in: downstreamResumeResponseInvalidResultCode},
		{name: "downstreamCloseResponseInvalidResultCode", in: downstreamCloseResponseInvalidResultCode},
		{name: "upstreamDataPointsAckInvalidResultCode", in: upstreamDataPointsAckInvalidResultCode},
		{name: "downstreamDataPointsAckInvalidResultCode", in: downstreamDataPointsAckInvalidResultCode},
		{name: "upstreamMetadataAckInvalidResultCode", in: upstreamMetadataAckInvalidResultCode},
		{name: "downstreamMetadataAckInvalidResultCode", in: downstreamMetadataAckInvalidResultCode},
		{name: "downstreamMetadataInvalidUpstreamQoS", in: downstreamMetadataInvalidUpstreamQoS},
		{name: "downstreamMetadataInvalidDownstreamOpenQoS", in: downstreamMetadataInvalidDownstreamOpenQoS},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := WireToProto(tt.in)
			assert.ErrorIs(t, err, errors.ErrMalformedMessage)
			assert.Nil(t, v)
		})
	}
}
