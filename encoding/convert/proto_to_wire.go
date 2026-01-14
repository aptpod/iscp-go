package convert

import (
	"time"

	autogen "github.com/aptpod/iscp-proto/gen/gogofast/iscp2/v1"
	autogenextensions "github.com/aptpod/iscp-proto/gen/gogofast/iscp2/v1/extensions"
	uuid "github.com/google/uuid"

	"github.com/aptpod/iscp-go/errors"
	"github.com/aptpod/iscp-go/message"
)

//nolint:gocyclo
func ProtoToWire(in *autogen.Message) (message.Message, error) {
	switch msg := in.GetMessage().(type) {
	case *autogen.Message_ConnectRequest:
		ext, err := toConnectRequestExtensionFields(msg.ConnectRequest.ExtensionFields)
		if err != nil {
			return nil, errorConvertToWire(msg.ConnectRequest, err)
		}
		return &message.ConnectRequest{
			RequestID:       message.RequestID(msg.ConnectRequest.RequestId),
			ProtocolVersion: msg.ConnectRequest.ProtocolVersion,
			NodeID:          msg.ConnectRequest.NodeId,
			PingInterval:    time.Duration(msg.ConnectRequest.PingInterval) * time.Second,
			PingTimeout:     time.Duration(msg.ConnectRequest.PingTimeout) * time.Second,
			ExtensionFields: ext,
		}, nil
	case *autogen.Message_ConnectResponse:
		rc, err := toResultCode(msg.ConnectResponse.ResultCode)
		if err != nil {
			return nil, errorConvertToWire(msg.ConnectResponse, err)
		}
		return &message.ConnectResponse{
			RequestID:       message.RequestID(msg.ConnectResponse.RequestId),
			ProtocolVersion: msg.ConnectResponse.ProtocolVersion,
			ResultCode:      rc,
			ResultString:    msg.ConnectResponse.ResultString,
			ExtensionFields: toConnectResponseExtensionFields(msg.ConnectResponse.ExtensionFields),
		}, nil
	case *autogen.Message_Disconnect:
		rc, err := toResultCode(msg.Disconnect.ResultCode)
		if err != nil {
			return nil, errorConvertToWire(msg.Disconnect, err)
		}
		return &message.Disconnect{
			ResultCode:      rc,
			ResultString:    msg.Disconnect.ResultString,
			ExtensionFields: toDisconnectExtensionFields(msg.Disconnect.ExtensionFields),
		}, nil
	case *autogen.Message_UpstreamOpenRequest:
		qos, err := toQoS(msg.UpstreamOpenRequest.Qos)
		if err != nil {
			return nil, errorConvertToWire(msg.UpstreamOpenRequest, err)
		}
		return &message.UpstreamOpenRequest{
			RequestID:         message.RequestID(msg.UpstreamOpenRequest.RequestId),
			SessionID:         msg.UpstreamOpenRequest.SessionId,
			AckInterval:       time.Duration(msg.UpstreamOpenRequest.AckInterval) * time.Millisecond,
			ExpiryInterval:    time.Duration(msg.UpstreamOpenRequest.ExpiryInterval) * time.Second,
			DataIDs:           toDataIDs(msg.UpstreamOpenRequest.DataIds),
			QoS:               qos,
			ExtensionFields:   toUpstreamOpenRequestExtensionFields(msg.UpstreamOpenRequest.ExtensionFields),
			EnableResumeToken: msg.UpstreamOpenRequest.EnableResumeToken,
		}, nil
	case *autogen.Message_UpstreamOpenResponse:
		rc, err := toResultCode(msg.UpstreamOpenResponse.ResultCode)
		if err != nil {
			return nil, errorConvertToWire(msg.UpstreamOpenResponse, err)
		}
		sid, err := toUUID(msg.UpstreamOpenResponse.AssignedStreamId)
		if err != nil {
			return nil, errorConvertToWire(msg.UpstreamOpenResponse, err)
		}
		return &message.UpstreamOpenResponse{
			RequestID:             message.RequestID(msg.UpstreamOpenResponse.RequestId),
			AssignedStreamID:      sid,
			AssignedStreamIDAlias: msg.UpstreamOpenResponse.AssignedStreamIdAlias,
			ResultCode:            rc,
			ResultString:          msg.UpstreamOpenResponse.ResultString,
			ServerTime:            time.Unix(0, msg.UpstreamOpenResponse.ServerTime).UTC(),
			DataIDAliases:         toDataIDAliases(msg.UpstreamOpenResponse.DataIdAliases),
			ExtensionFields:       toUpstreamOpenResponseExtensionFields(msg.UpstreamOpenResponse.ExtensionFields),
			ResumeToken:           msg.UpstreamOpenResponse.ResumeToken,
		}, nil
	case *autogen.Message_UpstreamResumeRequest:
		sid, err := toUUID(msg.UpstreamResumeRequest.StreamId)
		if err != nil {
			return nil, errorConvertToWire(msg.UpstreamResumeRequest, err)
		}
		return &message.UpstreamResumeRequest{
			RequestID:       message.RequestID(msg.UpstreamResumeRequest.RequestId),
			StreamID:        sid,
			ExtensionFields: toUpstreamResumeRequestExtensionFields(msg.UpstreamResumeRequest.ExtensionFields),
			ResumeToken:     msg.UpstreamResumeRequest.ResumeToken,
		}, nil
	case *autogen.Message_UpstreamResumeResponse:
		rc, err := toResultCode(msg.UpstreamResumeResponse.ResultCode)
		if err != nil {
			return nil, errorConvertToWire(msg.UpstreamResumeResponse, err)
		}
		return &message.UpstreamResumeResponse{
			RequestID:             message.RequestID(msg.UpstreamResumeResponse.RequestId),
			AssignedStreamIDAlias: msg.UpstreamResumeResponse.AssignedStreamIdAlias,
			ResultCode:            rc,
			ResultString:          msg.UpstreamResumeResponse.ResultString,
			ExtensionFields:       toUpstreamResumeResponseExtensionFields(msg.UpstreamResumeResponse.ExtensionFields),
			ResumeToken:           msg.UpstreamResumeResponse.ResumeToken,
		}, nil
	case *autogen.Message_UpstreamCloseRequest:
		sid, err := toUUID(msg.UpstreamCloseRequest.StreamId)
		if err != nil {
			return nil, errorConvertToWire(msg.UpstreamCloseRequest, err)
		}
		return &message.UpstreamCloseRequest{
			RequestID:           message.RequestID(msg.UpstreamCloseRequest.RequestId),
			StreamID:            sid,
			TotalDataPoints:     msg.UpstreamCloseRequest.TotalDataPoints,
			FinalSequenceNumber: msg.UpstreamCloseRequest.FinalSequenceNumber,
			ExtensionFields:     toUpstreamCloseRequestExtensionFields(msg.UpstreamCloseRequest.ExtensionFields),
		}, nil
	case *autogen.Message_UpstreamCloseResponse:
		rc, err := toResultCode(msg.UpstreamCloseResponse.ResultCode)
		if err != nil {
			return nil, errorConvertToWire(msg.UpstreamCloseResponse, err)
		}
		return &message.UpstreamCloseResponse{
			RequestID:       message.RequestID(msg.UpstreamCloseResponse.RequestId),
			ResultCode:      rc,
			ResultString:    msg.UpstreamCloseResponse.ResultString,
			ExtensionFields: toUpstreamCloseResponseExtensionFields(msg.UpstreamCloseResponse.ExtensionFields),
		}, nil
	case *autogen.Message_DownstreamOpenRequest:
		qos, err := toQoS(msg.DownstreamOpenRequest.Qos)
		if err != nil {
			return nil, errorConvertToWire(msg.DownstreamOpenRequest, err)
		}
		return &message.DownstreamOpenRequest{
			RequestID:            message.RequestID(msg.DownstreamOpenRequest.RequestId),
			DesiredStreamIDAlias: msg.DownstreamOpenRequest.DesiredStreamIdAlias,
			DownstreamFilters:    toDownstreamFilters(msg.DownstreamOpenRequest.DownstreamFilters),
			ExpiryInterval:       time.Duration(msg.DownstreamOpenRequest.ExpiryInterval) * time.Second,
			DataIDAliases:        toDataIDAliases(msg.DownstreamOpenRequest.DataIdAliases),
			QoS:                  qos,
			OmitEmptyChunk:       msg.DownstreamOpenRequest.OmitEmptyChunk,
			ExtensionFields:      toDownstreamOpenRequestExtensionFields(msg.DownstreamOpenRequest.ExtensionFields),
			EnableResumeToken:    msg.DownstreamOpenRequest.EnableResumeToken,
		}, nil
	case *autogen.Message_DownstreamOpenResponse:
		rc, err := toResultCode(msg.DownstreamOpenResponse.ResultCode)
		if err != nil {
			return nil, errorConvertToWire(msg.DownstreamOpenResponse, err)
		}
		sid, err := toUUID(msg.DownstreamOpenResponse.AssignedStreamId)
		if err != nil {
			return nil, errorConvertToWire(msg.DownstreamOpenResponse, err)
		}
		return &message.DownstreamOpenResponse{
			RequestID:        message.RequestID(msg.DownstreamOpenResponse.RequestId),
			AssignedStreamID: sid,
			ResultCode:       rc,
			ServerTime:       time.Unix(0, msg.DownstreamOpenResponse.ServerTime).UTC(),
			ResultString:     msg.DownstreamOpenResponse.ResultString,
			ExtensionFields:  toDownstreamOpenResponseExtensionFields(msg.DownstreamOpenResponse.ExtensionFields),
			ResumeToken:      msg.DownstreamOpenResponse.ResumeToken,
		}, nil
	case *autogen.Message_DownstreamResumeRequest:
		sid, err := toUUID(msg.DownstreamResumeRequest.StreamId)
		if err != nil {
			return nil, errorConvertToWire(msg.DownstreamResumeRequest, err)
		}
		return &message.DownstreamResumeRequest{
			RequestID:            message.RequestID(msg.DownstreamResumeRequest.RequestId),
			StreamID:             sid,
			DesiredStreamIDAlias: msg.DownstreamResumeRequest.DesiredStreamIdAlias,
			ExtensionFields:      toDownstreamResumeRequestExtensionFields(msg.DownstreamResumeRequest.ExtensionFields),
			ResumeToken:          msg.DownstreamResumeRequest.ResumeToken,
		}, nil
	case *autogen.Message_DownstreamResumeResponse:
		rc, err := toResultCode(msg.DownstreamResumeResponse.ResultCode)
		if err != nil {
			return nil, errorConvertToWire(msg.DownstreamResumeResponse, err)
		}
		return &message.DownstreamResumeResponse{
			RequestID:       message.RequestID(msg.DownstreamResumeResponse.RequestId),
			ResultCode:      rc,
			ResultString:    msg.DownstreamResumeResponse.ResultString,
			ExtensionFields: toDownstreamResumeResponseExtensionFields(msg.DownstreamResumeResponse.ExtensionFields),
			ResumeToken:     msg.DownstreamResumeResponse.ResumeToken,
		}, nil
	case *autogen.Message_DownstreamCloseRequest:
		sid, err := toUUID(msg.DownstreamCloseRequest.StreamId)
		if err != nil {
			return nil, errorConvertToWire(msg.DownstreamCloseRequest, err)
		}
		return &message.DownstreamCloseRequest{
			RequestID:       message.RequestID(msg.DownstreamCloseRequest.RequestId),
			StreamID:        sid,
			ExtensionFields: toDownstreamCloseRequestExtensionFields(msg.DownstreamCloseRequest.ExtensionFields),
		}, nil
	case *autogen.Message_DownstreamCloseResponse:
		rc, err := toResultCode(msg.DownstreamCloseResponse.ResultCode)
		if err != nil {
			return nil, errorConvertToWire(msg.DownstreamCloseResponse, err)
		}
		return &message.DownstreamCloseResponse{
			RequestID:       message.RequestID(msg.DownstreamCloseResponse.RequestId),
			ResultCode:      rc,
			ResultString:    msg.DownstreamCloseResponse.ResultString,
			ExtensionFields: toDownstreamCloseResponseExtensionFields(msg.DownstreamCloseResponse.ExtensionFields),
		}, nil
	case *autogen.Message_UpstreamCall:
		return &message.UpstreamCall{
			CallID:            msg.UpstreamCall.CallId,
			RequestCallID:     msg.UpstreamCall.RequestCallId,
			DestinationNodeID: msg.UpstreamCall.DestinationNodeId,
			Name:              msg.UpstreamCall.Name,
			Type:              msg.UpstreamCall.Type,
			Payload:           msg.UpstreamCall.Payload,
			ExtensionFields:   toUpstreamCallExtensionFields(msg.UpstreamCall.ExtensionFields),
		}, nil
	case *autogen.Message_UpstreamCallAck:
		rc, err := toResultCode(msg.UpstreamCallAck.ResultCode)
		if err != nil {
			return nil, errorConvertToWire(msg.UpstreamCallAck, err)
		}
		return &message.UpstreamCallAck{
			CallID:          msg.UpstreamCallAck.CallId,
			ResultCode:      rc,
			ResultString:    msg.UpstreamCallAck.ResultString,
			ExtensionFields: toUpstreamCallAckExtensionFields(msg.UpstreamCallAck.ExtensionFields),
		}, nil
	case *autogen.Message_DownstreamCall:
		return &message.DownstreamCall{
			CallID:          msg.DownstreamCall.CallId,
			RequestCallID:   msg.DownstreamCall.RequestCallId,
			SourceNodeID:    msg.DownstreamCall.SourceNodeId,
			Name:            msg.DownstreamCall.Name,
			Type:            msg.DownstreamCall.Type,
			Payload:         msg.DownstreamCall.Payload,
			ExtensionFields: toDownstreamCallExtensionFields(msg.DownstreamCall.ExtensionFields),
		}, nil
	case *autogen.Message_Ping:
		return &message.Ping{
			RequestID:       message.RequestID(msg.Ping.RequestId),
			ExtensionFields: toPingExtensionFields(msg.Ping.ExtensionFields),
		}, nil
	case *autogen.Message_Pong:
		return &message.Pong{
			RequestID:       message.RequestID(msg.Pong.RequestId),
			ExtensionFields: toPongExtensionFields(msg.Pong.ExtensionFields),
		}, nil

		// ストリームメッセージ
	case *autogen.Message_UpstreamChunk:
		sc, err := toStreamChunk(msg.UpstreamChunk.StreamChunk)
		if err != nil {
			return nil, errorConvertToWire(msg.UpstreamChunk, err)
		}
		return &message.UpstreamChunk{
			StreamIDAlias:   msg.UpstreamChunk.StreamIdAlias,
			DataIDs:         toDataIDs(msg.UpstreamChunk.DataIds),
			StreamChunk:     sc,
			ExtensionFields: toUpstreamChunkExtensionFields(msg.UpstreamChunk.ExtensionFields),
		}, nil
	case *autogen.Message_UpstreamChunkAck:
		res, err := toUpstreamDataPointResults(msg.UpstreamChunkAck.Results)
		if err != nil {
			return nil, errorConvertToWire(msg.UpstreamChunkAck, err)
		}
		return &message.UpstreamChunkAck{
			StreamIDAlias:   msg.UpstreamChunkAck.StreamIdAlias,
			Results:         res,
			ExtensionFields: toUpstreamChunkAckExtensionFields(msg.UpstreamChunkAck.ExtensionFields),
			DataIDAliases:   toDataIDAliases(msg.UpstreamChunkAck.DataIdAliases),
		}, nil
	case *autogen.Message_DownstreamChunk:
		upstreamOrAlias, err := toUpstreamOrAlias(msg.DownstreamChunk)
		if err != nil {
			return nil, errorConvertToWire(msg.DownstreamChunk, err)
		}
		sc, err := toStreamChunk(msg.DownstreamChunk.StreamChunk)
		if err != nil {
			return nil, errorConvertToWire(msg.DownstreamChunk, err)
		}

		return &message.DownstreamChunk{
			StreamIDAlias:              msg.DownstreamChunk.StreamIdAlias,
			UpstreamOrAlias:            upstreamOrAlias,
			StreamChunk:                sc,
			ExtensionFields:            toDownstreamChunkExtensionFields(msg.DownstreamChunk.ExtensionFields),
			DownstreamFilterReferences: toDownstreamFilterReferencesWire(msg.DownstreamChunk.DownstreamFilterReferences),
		}, nil
	case *autogen.Message_DownstreamChunkAck:
		res, err := toDownstreamDataPointResults(msg.DownstreamChunkAck.Results)
		if err != nil {
			return nil, errorConvertToWire(msg.DownstreamChunkAck, err)
		}
		return &message.DownstreamChunkAck{
			StreamIDAlias:   msg.DownstreamChunkAck.StreamIdAlias,
			AckID:           msg.DownstreamChunkAck.AckId,
			Results:         res,
			UpstreamAliases: toUpstreamAliases(msg.DownstreamChunkAck.UpstreamAliases),
			DataIDAliases:   toDataIDAliases(msg.DownstreamChunkAck.DataIdAliases),
			ExtensionFields: toDownstreamChunkAckExtensionFields(msg.DownstreamChunkAck.ExtensionFields),
		}, nil
	case *autogen.Message_UpstreamMetadata:
		metadataUpstream, err := toMetadataUpstream(msg.UpstreamMetadata)
		if err != nil {
			return nil, errorConvertToWire(msg.UpstreamMetadata, err)
		}
		return &message.UpstreamMetadata{
			RequestID:       message.RequestID(msg.UpstreamMetadata.RequestId),
			Metadata:        metadataUpstream,
			ExtensionFields: toUpstreamMetadataExtensionFields(msg.UpstreamMetadata.ExtensionFields),
		}, nil
	case *autogen.Message_UpstreamMetadataAck:
		rc, err := toResultCode(msg.UpstreamMetadataAck.ResultCode)
		if err != nil {
			return nil, errorConvertToWire(msg.UpstreamMetadataAck, err)
		}
		return &message.UpstreamMetadataAck{
			RequestID:       message.RequestID(msg.UpstreamMetadataAck.RequestId),
			ResultCode:      rc,
			ResultString:    msg.UpstreamMetadataAck.ResultString,
			ExtensionFields: toUpstreamMetadataAckExtensionFields(msg.UpstreamMetadataAck.ExtensionFields),
		}, nil
	case *autogen.Message_DownstreamMetadata:
		metadata, err := toDownstreamMetadata(msg.DownstreamMetadata)
		if err != nil {
			return nil, errorConvertToWire(msg.DownstreamMetadata, err)
		}
		return &message.DownstreamMetadata{
			RequestID:       message.RequestID(msg.DownstreamMetadata.RequestId),
			StreamIDAlias:   msg.DownstreamMetadata.StreamIdAlias,
			SourceNodeID:    msg.DownstreamMetadata.SourceNodeId,
			Metadata:        metadata,
			ExtensionFields: toDownstreamMetadataExtensionFields(msg.DownstreamMetadata.ExtensionFields),
		}, nil
	case *autogen.Message_DownstreamMetadataAck:
		rc, err := toResultCode(msg.DownstreamMetadataAck.ResultCode)
		if err != nil {
			return nil, errorConvertToWire(msg.DownstreamMetadataAck, err)
		}
		return &message.DownstreamMetadataAck{
			RequestID:       message.RequestID(msg.DownstreamMetadataAck.RequestId),
			ResultCode:      rc,
			ResultString:    msg.DownstreamMetadataAck.ResultString,
			ExtensionFields: toDownstreamMetadataAckExtensionFields(msg.DownstreamMetadataAck.ExtensionFields),
		}, nil

	case *autogen.Message_DownstreamChunkAckComplete:
		rc, err := toResultCode(msg.DownstreamChunkAckComplete.ResultCode)
		if err != nil {
			return nil, errorConvertToWire(msg.DownstreamChunkAckComplete, err)
		}
		return &message.DownstreamChunkAckComplete{
			StreamIDAlias:   msg.DownstreamChunkAckComplete.StreamIdAlias,
			AckID:           msg.DownstreamChunkAckComplete.AckId,
			ResultCode:      rc,
			ResultString:    msg.DownstreamChunkAckComplete.ResultString,
			ExtensionFields: toDownstreamChunkAckCompleteExtensionFields(msg.DownstreamChunkAckComplete.ExtensionFields),
		}, nil
	default:
		return nil, errors.Errorf("msg type:%T : %w", msg, errors.ErrMalformedMessage)
	}
}

func toResultCode(in autogen.ResultCode) (message.ResultCode, error) {
	switch in {
	case autogen.ResultCode_SUCCEEDED:
		return message.ResultCodeSucceeded, nil
	case autogen.ResultCode_INCOMPATIBLE_VERSION:
		return message.ResultCodeIncompatibleVersion, nil
	case autogen.ResultCode_MAXIMUM_DATA_ID_ALIAS:
		return message.ResultCodeMaximumDataIDAlias, nil
	case autogen.ResultCode_MAXIMUM_UPSTREAM_ALIAS:
		return message.ResultCodeMaximumUpstreamAlias, nil
	case autogen.ResultCode_UNSPECIFIED_ERROR:
		return message.ResultCodeUnspecifiedError, nil
	case autogen.ResultCode_NO_NODE_ID:
		return message.ResultCodeNoNodeID, nil
	case autogen.ResultCode_AUTH_FAILED:
		return message.ResultCodeAuthFailed, nil
	case autogen.ResultCode_CONNECT_TIMEOUT:
		return message.ResultCodeConnectTimeout, nil
	case autogen.ResultCode_MALFORMED_MESSAGE:
		return message.ResultCodeMalformedMessage, nil
	case autogen.ResultCode_PROTOCOL_ERROR:
		return message.ResultCodeProtocolError, nil
	case autogen.ResultCode_ACK_TIMEOUT:
		return message.ResultCodeAckTimeout, nil
	case autogen.ResultCode_INVALID_PAYLOAD:
		return message.ResultCodeInvalidPayload, nil
	case autogen.ResultCode_INVALID_DATA_ID:
		return message.ResultCodeInvalidDataID, nil
	case autogen.ResultCode_INVALID_DATA_ID_ALIAS:
		return message.ResultCodeInvalidDataIDAlias, nil
	case autogen.ResultCode_INVALID_DATA_FILTER:
		return message.ResultCodeInvalidDataFilter, nil
	case autogen.ResultCode_STREAM_NOT_FOUND:
		return message.ResultCodeStreamNotFound, nil
	case autogen.ResultCode_RESUME_REQUEST_CONFLICT:
		return message.ResultCodeResumeRequestConflict, nil
	case autogen.ResultCode_PROCESS_FAILED:
		return message.ResultCodeProcessFailed, nil
	case autogen.ResultCode_DESIRED_QOS_NOT_SUPPORTED:
		return message.ResultCodeDesiredQosNotSupported, nil
	case autogen.ResultCode_PING_TIMEOUT:
		return message.ResultCodePingTimeout, nil
	case autogen.ResultCode_TOO_LARGE_MESSAGE_SIZE:
		return message.ResultCodeTooLargeMessageSize, nil
	case autogen.ResultCode_TOO_MANY_DATA_ID_ALIASES:
		return message.ResultCodeTooManyDataIDAliases, nil
	case autogen.ResultCode_TOO_MANY_STREAMS:
		return message.ResultCodeTooManyStreams, nil
	case autogen.ResultCode_TOO_LONG_ACK_INTERVAL:
		return message.ResultCodeTooLongAckInterval, nil
	case autogen.ResultCode_TOO_MANY_DOWNSTREAM_FILTERS:
		return message.ResultCodeTooManyDownstreamFilters, nil
	case autogen.ResultCode_TOO_MANY_DATA_FILTERS:
		return message.ResultCodeTooManyDataFilters, nil
	case autogen.ResultCode_TOO_LONG_EXPIRY_INTERVAL:
		return message.ResultCodeTooLongExpiryInterval, nil
	case autogen.ResultCode_TOO_LONG_PING_TIMEOUT:
		return message.ResultCodeTooLongPingTimeout, nil
	case autogen.ResultCode_TOO_SHORT_PING_TIMEOUT:
		return message.ResultCodeTooShortPingTimeout, nil
	case autogen.ResultCode_NODE_ID_MISMATCH:
		return message.ResultCodeNodeIDMismatch, nil
	case autogen.ResultCode_RATE_LIMIT_REACHED:
		return message.ResultCodeRateLimitReached, nil
	case autogen.ResultCode_SESSION_NOT_FOUND:
		return message.ResultCodeSessionNotFound, nil
	case autogen.ResultCode_SESSION_ALREADY_CLOSED:
		return message.ResultCodeSessionAlreadyClosed, nil
	case autogen.ResultCode_SESSION_CANNOT_CLOSED:
		return message.ResultCodeSessionCannotClosed, nil
	case autogen.ResultCode_INVALID_RESUME_TOKEN:
		return message.ResultCodeInvalidResumeToken, nil
	}
	return 0, errors.Errorf("result_code:%v : %w", in, errors.ErrMalformedMessage)
}

func toDataIDs(in []*autogen.DataID) []*message.DataID {
	res := make([]*message.DataID, 0, len(in))
	for _, v := range in {
		res = append(res, &message.DataID{
			Name: v.Name,
			Type: v.Type,
		})
	}
	return res
}

func toDataIDAliases(in map[uint32]*autogen.DataID) map[uint32]*message.DataID {
	res := make(map[uint32]*message.DataID, len(in))
	for k, v := range in {
		res[k] = &message.DataID{
			Name: v.Name,
			Type: v.Type,
		}
	}
	return res
}

func toQoS(in autogen.QoS) (message.QoS, error) {
	switch in {
	case autogen.QoS_RELIABLE:
		return message.QoSReliable, nil
	case autogen.QoS_UNRELIABLE:
		return message.QoSUnreliable, nil
	case autogen.QoS_PARTIAL:
		return message.QoSPartial, nil
	}

	return 0, errors.Errorf("qos:%v : %w", in, errors.ErrMalformedMessage)
}

func toDownstreamFilters(in []*autogen.DownstreamFilter) []*message.DownstreamFilter {
	res := make([]*message.DownstreamFilter, 0, len(in))
	for _, v := range in {
		res = append(res, &message.DownstreamFilter{
			SourceNodeID: v.SourceNodeId,
			DataFilters:  toDataFilters(v.DataFilters),
		})
	}
	return res
}

func toDataFilters(in []*autogen.DataFilter) []*message.DataFilter {
	res := make([]*message.DataFilter, 0, len(in))
	for _, v := range in {
		res = append(res, &message.DataFilter{
			Name: v.Name,
			Type: v.Type,
		})
	}
	return res
}

func toDataPoints(in []*autogen.DataPoint) []*message.DataPoint {
	res := make([]*message.DataPoint, 0, len(in))
	for _, v := range in {
		res = append(res, &message.DataPoint{
			ElapsedTime: time.Duration(v.ElapsedTime),
			Payload:     v.Payload,
		})
	}
	return res
}

func toUpstreamDataPointResults(in []*autogen.UpstreamChunkResult) ([]*message.UpstreamChunkResult, error) {
	res := make([]*message.UpstreamChunkResult, 0, len(in))
	for _, v := range in {
		rs, err := toResultCode(v.ResultCode)
		if err != nil {
			return nil, err
		}
		res = append(res, &message.UpstreamChunkResult{
			SequenceNumber:  v.SequenceNumber,
			ResultCode:      rs,
			ResultString:    v.ResultString,
			ExtensionFields: toUpstreamChunkResultExtensionFields(v.ExtensionFields),
		})
	}
	return res, nil
}

func toDownstreamDataPointResults(in []*autogen.DownstreamChunkResult) ([]*message.DownstreamChunkResult, error) {
	res := make([]*message.DownstreamChunkResult, 0, len(in))
	for _, v := range in {
		rs, err := toResultCode(v.ResultCode)
		if err != nil {
			return nil, err
		}
		res = append(res, &message.DownstreamChunkResult{
			StreamIDOfUpstream:       uuid.Must(uuid.FromBytes(v.StreamIdOfUpstream)),
			SequenceNumberInUpstream: v.SequenceNumberInUpstream,
			ResultCode:               rs,
			ResultString:             v.ResultString,
			ExtensionFields:          toDownstreamChunkResultExtensionFields(v.ExtensionFields),
		})
	}
	return res, nil
}

func toDownstreamMetadata(in *autogen.DownstreamMetadata) (message.Metadata, error) {
	switch v := in.Metadata.(type) {
	case *autogen.DownstreamMetadata_BaseTime:
		return ToBaseTime(v.BaseTime), nil
	case *autogen.DownstreamMetadata_UpstreamOpen:
		return ToUpstreamOpen(v.UpstreamOpen)
	case *autogen.DownstreamMetadata_UpstreamAbnormalClose:
		return ToUpstreamAbnormalClose(v.UpstreamAbnormalClose)
	case *autogen.DownstreamMetadata_UpstreamResume:
		return ToUpstreamResume(v.UpstreamResume)
	case *autogen.DownstreamMetadata_UpstreamNormalClose:
		return ToUpstreamNormalClose(v.UpstreamNormalClose)
	case *autogen.DownstreamMetadata_DownstreamOpen:
		return ToDownstreamOpen(v.DownstreamOpen)
	case *autogen.DownstreamMetadata_DownstreamAbnormalClose:
		return ToDownstreamAbnormalClose(v.DownstreamAbnormalClose)
	case *autogen.DownstreamMetadata_DownstreamResume:
		return ToDownstreamResume(v.DownstreamResume)
	case *autogen.DownstreamMetadata_DownstreamNormalClose:
		return ToDownstreamNormalClose(v.DownstreamNormalClose)
	}
	return nil, errors.Errorf("downstream_metadata:%v : %w", in.Metadata, errors.ErrMalformedMessage)
}

func ToUpstreamOpen(v *autogen.UpstreamOpen) (*message.UpstreamOpen, error) {
	qos, err := toQoS(v.Qos)
	if err != nil {
		return nil, err
	}
	sid, err := toUUID(v.StreamId)
	if err != nil {
		return nil, err
	}
	return &message.UpstreamOpen{
		StreamID:  sid,
		SessionID: v.SessionId,
		QoS:       qos,
	}, nil
}

func ToUpstreamAbnormalClose(v *autogen.UpstreamAbnormalClose) (*message.UpstreamAbnormalClose, error) {
	sid, err := toUUID(v.StreamId)
	if err != nil {
		return nil, err
	}
	return &message.UpstreamAbnormalClose{
		StreamID:  sid,
		SessionID: v.SessionId,
	}, nil
}

func ToUpstreamResume(v *autogen.UpstreamResume) (*message.UpstreamResume, error) {
	sid, err := toUUID(v.StreamId)
	if err != nil {
		return nil, err
	}
	qos, err := toQoS(v.Qos)
	if err != nil {
		return nil, err
	}
	return &message.UpstreamResume{
		StreamID:  sid,
		QoS:       qos,
		SessionID: v.SessionId,
	}, nil
}

func ToUpstreamNormalClose(v *autogen.UpstreamNormalClose) (*message.UpstreamNormalClose, error) {
	sid, err := toUUID(v.StreamId)
	if err != nil {
		return nil, err
	}
	return &message.UpstreamNormalClose{
		StreamID:            sid,
		SessionID:           v.SessionId,
		TotalDataPoints:     v.TotalDataPoints,
		FinalSequenceNumber: v.FinalSequenceNumber,
	}, nil
}

func ToDownstreamOpen(v *autogen.DownstreamOpen) (*message.DownstreamOpen, error) {
	qos, err := toQoS(v.Qos)
	if err != nil {
		return nil, err
	}
	sid, err := toUUID(v.StreamId)
	if err != nil {
		return nil, err
	}
	return &message.DownstreamOpen{
		StreamID:          sid,
		DownstreamFilters: toDownstreamFilters(v.DownstreamFilters),
		QoS:               qos,
	}, nil
}

func ToDownstreamAbnormalClose(v *autogen.DownstreamAbnormalClose) (*message.DownstreamAbnormalClose, error) {
	sid, err := toUUID(v.StreamId)
	if err != nil {
		return nil, err
	}
	return &message.DownstreamAbnormalClose{
		StreamID: sid,
	}, nil
}

func ToDownstreamResume(v *autogen.DownstreamResume) (*message.DownstreamResume, error) {
	sid, err := toUUID(v.StreamId)
	if err != nil {
		return nil, err
	}
	qos, err := toQoS(v.Qos)
	if err != nil {
		return nil, err
	}
	return &message.DownstreamResume{
		StreamID:          sid,
		DownstreamFilters: toDownstreamFilters(v.DownstreamFilters),
		QoS:               qos,
	}, nil
}

func ToDownstreamNormalClose(v *autogen.DownstreamNormalClose) (*message.DownstreamNormalClose, error) {
	sid, err := toUUID(v.StreamId)
	if err != nil {
		return nil, err
	}
	return &message.DownstreamNormalClose{
		StreamID: sid,
	}, nil
}

func toMetadataUpstream(in *autogen.UpstreamMetadata) (message.SendableMetadata, error) {
	switch v := in.Metadata.(type) {
	case *autogen.UpstreamMetadata_BaseTime:
		return ToBaseTime(v.BaseTime), nil
	}
	return nil, errors.Errorf("upstream_metadata:%v : %w", in.Metadata, errors.ErrMalformedMessage)
}

func ToBaseTime(v *autogen.BaseTime) *message.BaseTime {
	return &message.BaseTime{
		SessionID:   v.SessionId,
		Name:        v.Name,
		Priority:    uint8(v.Priority),
		ElapsedTime: time.Duration(v.ElapsedTime),
		BaseTime:    time.Unix(0, v.BaseTime).UTC(),
	}
}

func toUpstreamOrAlias(in *autogen.DownstreamChunk) (message.UpstreamOrAlias, error) {
	switch v := in.UpstreamOrAlias.(type) {
	case *autogen.DownstreamChunk_UpstreamInfo:
		sid, err := toUUID(v.UpstreamInfo.StreamId)
		if err != nil {
			return nil, err
		}
		return &message.UpstreamInfo{
			SourceNodeID: v.UpstreamInfo.SourceNodeId,
			SessionID:    v.UpstreamInfo.SessionId,
			StreamID:     sid,
		}, nil
	case *autogen.DownstreamChunk_UpstreamAlias:
		return message.UpstreamAlias(v.UpstreamAlias), nil
	}
	return nil, errors.Errorf("upstream_or_alias:%v : %w", in, errors.ErrMalformedMessage)
}

func toUUID(in []byte) (uuid.UUID, error) {
	res, err := uuid.FromBytes(in)
	if err != nil {
		return uuid.UUID{}, errors.Errorf("stream_id:%x : %w", in, errors.ErrMalformedMessage)
	}
	return res, nil
}

func toConnectRequestExtensionFields(in *autogenextensions.ConnectRequestExtensionFields) (*message.ConnectRequestExtensionFields, error) {
	if in == nil {
		return nil, nil
	}
	intdashFields, err := toIntdashExtensionFields(in.Intdash)
	if err != nil {
		return nil, err
	}
	return &message.ConnectRequestExtensionFields{
		AccessToken: in.AccessToken,
		Intdash:     intdashFields,
	}, nil
}

func toIntdashExtensionFields(in *autogenextensions.IntdashExtensionFields) (*message.IntdashExtensionFields, error) {
	if in == nil {
		return nil, nil
	}
	projectUUID, err := uuid.Parse(in.ProjectUuid)
	if err != nil {
		return nil, errors.Errorf("failed to parse a project uuid. in: %s  cause: %+v: %w", in.ProjectUuid, err, errors.ErrMalformedMessage)
	}
	return &message.IntdashExtensionFields{
		ProjectUUID: projectUUID,
	}, nil
}

func toConnectResponseExtensionFields(in *autogenextensions.ConnectResponseExtensionFields) *message.ConnectResponseExtensionFields {
	if in == nil {
		return nil
	}
	return &message.ConnectResponseExtensionFields{}
}

func toDisconnectExtensionFields(in *autogenextensions.DisconnectExtensionFields) *message.DisconnectExtensionFields {
	if in == nil {
		return nil
	}
	return &message.DisconnectExtensionFields{}
}

func toUpstreamOpenRequestExtensionFields(in *autogenextensions.UpstreamOpenRequestExtensionFields) *message.UpstreamOpenRequestExtensionFields {
	if in == nil {
		return nil
	}
	return &message.UpstreamOpenRequestExtensionFields{
		Persist: in.Persist,
	}
}

func toUpstreamOpenResponseExtensionFields(in *autogenextensions.UpstreamOpenResponseExtensionFields) *message.UpstreamOpenResponseExtensionFields {
	if in == nil {
		return nil
	}
	return &message.UpstreamOpenResponseExtensionFields{}
}

func toUpstreamResumeRequestExtensionFields(in *autogenextensions.UpstreamResumeRequestExtensionFields) *message.UpstreamResumeRequestExtensionFields {
	if in == nil {
		return nil
	}
	return &message.UpstreamResumeRequestExtensionFields{}
}

func toUpstreamResumeResponseExtensionFields(in *autogenextensions.UpstreamResumeResponseExtensionFields) *message.UpstreamResumeResponseExtensionFields {
	if in == nil {
		return nil
	}
	return &message.UpstreamResumeResponseExtensionFields{}
}

func toUpstreamCloseRequestExtensionFields(in *autogenextensions.UpstreamCloseRequestExtensionFields) *message.UpstreamCloseRequestExtensionFields {
	if in == nil {
		return nil
	}
	return &message.UpstreamCloseRequestExtensionFields{
		CloseSession: in.CloseSession,
	}
}

func toUpstreamCloseResponseExtensionFields(in *autogenextensions.UpstreamCloseResponseExtensionFields) *message.UpstreamCloseResponseExtensionFields {
	if in == nil {
		return nil
	}
	return &message.UpstreamCloseResponseExtensionFields{}
}

func toDownstreamOpenRequestExtensionFields(in *autogenextensions.DownstreamOpenRequestExtensionFields) *message.DownstreamOpenRequestExtensionFields {
	if in == nil {
		return nil
	}
	return &message.DownstreamOpenRequestExtensionFields{}
}

func toDownstreamOpenResponseExtensionFields(in *autogenextensions.DownstreamOpenResponseExtensionFields) *message.DownstreamOpenResponseExtensionFields {
	if in == nil {
		return nil
	}
	return &message.DownstreamOpenResponseExtensionFields{}
}

func toDownstreamResumeRequestExtensionFields(in *autogenextensions.DownstreamResumeRequestExtensionFields) *message.DownstreamResumeRequestExtensionFields {
	if in == nil {
		return nil
	}
	return &message.DownstreamResumeRequestExtensionFields{}
}

func toDownstreamResumeResponseExtensionFields(in *autogenextensions.DownstreamResumeResponseExtensionFields) *message.DownstreamResumeResponseExtensionFields {
	if in == nil {
		return nil
	}
	return &message.DownstreamResumeResponseExtensionFields{}
}

func toDownstreamCloseRequestExtensionFields(in *autogenextensions.DownstreamCloseRequestExtensionFields) *message.DownstreamCloseRequestExtensionFields {
	if in == nil {
		return nil
	}
	return &message.DownstreamCloseRequestExtensionFields{}
}

func toDownstreamCloseResponseExtensionFields(in *autogenextensions.DownstreamCloseResponseExtensionFields) *message.DownstreamCloseResponseExtensionFields {
	if in == nil {
		return nil
	}
	return &message.DownstreamCloseResponseExtensionFields{}
}

func toUpstreamCallExtensionFields(in *autogenextensions.UpstreamCallExtensionFields) *message.UpstreamCallExtensionFields {
	if in == nil {
		return nil
	}
	return &message.UpstreamCallExtensionFields{}
}

func toUpstreamCallAckExtensionFields(in *autogenextensions.UpstreamCallAckExtensionFields) *message.UpstreamCallAckExtensionFields {
	if in == nil {
		return nil
	}
	return &message.UpstreamCallAckExtensionFields{}
}

func toDownstreamCallExtensionFields(in *autogenextensions.DownstreamCallExtensionFields) *message.DownstreamCallExtensionFields {
	if in == nil {
		return nil
	}
	return &message.DownstreamCallExtensionFields{}
}

func toPingExtensionFields(in *autogenextensions.PingExtensionFields) *message.PingExtensionFields {
	if in == nil {
		return nil
	}
	return &message.PingExtensionFields{}
}

func toPongExtensionFields(in *autogenextensions.PongExtensionFields) *message.PongExtensionFields {
	if in == nil {
		return nil
	}
	return &message.PongExtensionFields{}
}

func toUpstreamChunkExtensionFields(in *autogenextensions.UpstreamChunkExtensionFields) *message.UpstreamChunkExtensionFields {
	if in == nil {
		return nil
	}
	return &message.UpstreamChunkExtensionFields{}
}

func toUpstreamChunkAckExtensionFields(in *autogenextensions.UpstreamChunkAckExtensionFields) *message.UpstreamChunkAckExtensionFields {
	if in == nil {
		return nil
	}
	return &message.UpstreamChunkAckExtensionFields{}
}

func toDownstreamChunkExtensionFields(in *autogenextensions.DownstreamChunkExtensionFields) *message.DownstreamChunkExtensionFields {
	if in == nil {
		return nil
	}
	return &message.DownstreamChunkExtensionFields{}
}

func toDownstreamChunkAckExtensionFields(in *autogenextensions.DownstreamChunkAckExtensionFields) *message.DownstreamChunkAckExtensionFields {
	if in == nil {
		return nil
	}
	return &message.DownstreamChunkAckExtensionFields{}
}

func toUpstreamMetadataExtensionFields(in *autogenextensions.UpstreamMetadataExtensionFields) *message.UpstreamMetadataExtensionFields {
	if in == nil {
		return nil
	}
	return &message.UpstreamMetadataExtensionFields{
		Persist: in.Persist,
	}
}

func toUpstreamMetadataAckExtensionFields(in *autogenextensions.UpstreamMetadataAckExtensionFields) *message.UpstreamMetadataAckExtensionFields {
	if in == nil {
		return nil
	}
	return &message.UpstreamMetadataAckExtensionFields{}
}

func toDownstreamMetadataExtensionFields(in *autogenextensions.DownstreamMetadataExtensionFields) *message.DownstreamMetadataExtensionFields {
	if in == nil {
		return nil
	}
	return &message.DownstreamMetadataExtensionFields{}
}

func toDownstreamMetadataAckExtensionFields(in *autogenextensions.DownstreamMetadataAckExtensionFields) *message.DownstreamMetadataAckExtensionFields {
	if in == nil {
		return nil
	}
	return &message.DownstreamMetadataAckExtensionFields{}
}

func toUpstreamChunkResultExtensionFields(in *autogenextensions.UpstreamChunkResultExtensionFields) *message.UpstreamChunkResultExtensionFields {
	if in == nil {
		return nil
	}
	return &message.UpstreamChunkResultExtensionFields{}
}

func toDownstreamChunkResultExtensionFields(in *autogenextensions.DownstreamChunkResultExtensionFields) *message.DownstreamChunkResultExtensionFields {
	if in == nil {
		return nil
	}
	return &message.DownstreamChunkResultExtensionFields{}
}

func toDownstreamChunkAckCompleteExtensionFields(in *autogenextensions.DownstreamChunkAckCompleteExtensionFields) *message.DownstreamChunkAckCompleteExtensionFields {
	if in == nil {
		return nil
	}
	return &message.DownstreamChunkAckCompleteExtensionFields{}
}

func toUpstreamAliases(in map[uint32]*autogen.UpstreamInfo) map[uint32]*message.UpstreamInfo {
	res := make(map[uint32]*message.UpstreamInfo, len(in))
	for k, v := range in {
		res[k] = toUpstreamInfo(v)
	}
	return res
}

func toUpstreamInfo(in *autogen.UpstreamInfo) *message.UpstreamInfo {
	if in == nil {
		return &message.UpstreamInfo{}
	}
	return &message.UpstreamInfo{
		SessionID:    in.SessionId,
		SourceNodeID: in.SourceNodeId,
		StreamID:     uuid.Must(uuid.FromBytes(in.StreamId)),
	}
}

func toStreamChunk(in *autogen.StreamChunk) (*message.StreamChunk, error) {
	if in == nil {
		return &message.StreamChunk{}, nil
	}
	dpgs, err := toDataPointGroups(in.DataPointGroups)
	if err != nil {
		return nil, err
	}
	return &message.StreamChunk{
		SequenceNumber:  in.SequenceNumber,
		DataPointGroups: dpgs,
	}, nil
}

func toDataPointGroups(in []*autogen.DataPointGroup) ([]*message.DataPointGroup, error) {
	res := make([]*message.DataPointGroup, 0, len(in))
	for _, v := range in {
		dpg, err := toDataPointGroup(v)
		if err != nil {
			return nil, err
		}
		res = append(res, dpg)
	}
	return res, nil
}

func toDataPointGroup(in *autogen.DataPointGroup) (*message.DataPointGroup, error) {
	if in == nil {
		return &message.DataPointGroup{}, nil
	}
	dataIDOrAlias, err := toDataIDOrAlias(in.DataIdOrAlias)
	if err != nil {
		return nil, err
	}
	return &message.DataPointGroup{
		DataIDOrAlias: dataIDOrAlias,
		DataPoints:    toDataPoints(in.DataPoints),
	}, nil
}

func toDataIDOrAlias(in interface{}) (message.DataIDOrAlias, error) {
	switch v := in.(type) {
	case autogen.DataPointGroup_DataId:
		return &message.DataID{
			Name: v.DataId.Name,
			Type: v.DataId.Type,
		}, nil
	case *autogen.DataPointGroup_DataId:
		return &message.DataID{
			Name: v.DataId.Name,
			Type: v.DataId.Type,
		}, nil
	case autogen.DataPointGroup_DataIdAlias:
		return message.DataIDAlias(v.DataIdAlias), nil
	case *autogen.DataPointGroup_DataIdAlias:
		return message.DataIDAlias(v.DataIdAlias), nil
	}
	return nil, errors.Errorf("invalid DataIDOrAlias %v %T", in, in)
}

func toDownstreamFilterReferencesWire(in []*autogen.DownstreamFilterReferences) [][]*message.DownstreamFilterReference {
	if in == nil {
		return nil
	}
	res := make([][]*message.DownstreamFilterReference, 0, len(in))
	for _, group := range in {
		if group != nil {
			refs := make([]*message.DownstreamFilterReference, 0, len(group.References))
			for _, v := range group.References {
				if v != nil {
					refs = append(refs, &message.DownstreamFilterReference{
						DownstreamFilterIndex: v.DownstreamFilterIndex,
						DataFilterIndex:       v.DataFilterIndex,
					})
				}
			}
			res = append(res, refs)
		}
	}
	return res
}

func errorConvertToWire(m any, err error) error {
	return errors.Errorf("failed to protobuf %T: %w", m, err)
}
