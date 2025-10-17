package convert

import (
	"github.com/aptpod/iscp-go/errors"
	"github.com/aptpod/iscp-go/message"
	autogen "github.com/aptpod/iscp-proto/gen/gogofast/iscp2/v1"
	autogenextensions "github.com/aptpod/iscp-proto/gen/gogofast/iscp2/v1/extensions"
)

//nolint:gocyclo
func WireToProto(in message.Message) (*autogen.Message, error) {
	switch msg := in.(type) {
	case *message.ConnectRequest:
		return &autogen.Message{Message: &autogen.Message_ConnectRequest{
			ConnectRequest: &autogen.ConnectRequest{
				RequestId:       uint32(msg.RequestID),
				ProtocolVersion: msg.ProtocolVersion,
				NodeId:          msg.NodeID,
				PingInterval:    uint32(msg.PingInterval.Seconds()),
				PingTimeout:     uint32(msg.PingTimeout.Seconds()),
				ExtensionFields: toConnectRequestExtensionFieldsProto(msg.ExtensionFields),
			},
		}}, nil
	case *message.ConnectResponse:
		rc, err := toResultCodeProto(msg.ResultCode)
		if err != nil {
			return nil, errorConvertToProto(msg, err)
		}
		return &autogen.Message{Message: &autogen.Message_ConnectResponse{
			ConnectResponse: &autogen.ConnectResponse{
				RequestId:       uint32(msg.RequestID),
				ProtocolVersion: msg.ProtocolVersion,
				ResultCode:      rc,
				ResultString:    msg.ResultString,
				ExtensionFields: toConnectResponseExtensionFieldsProto(msg.ExtensionFields),
			},
		}}, nil
	case *message.Disconnect:
		rc, err := toResultCodeProto(msg.ResultCode)
		if err != nil {
			return nil, errorConvertToProto(msg, err)
		}
		return &autogen.Message{Message: &autogen.Message_Disconnect{
			Disconnect: &autogen.Disconnect{
				ResultCode:      rc,
				ResultString:    msg.ResultString,
				ExtensionFields: toDisconnectExtensionFieldsProto(msg.ExtensionFields),
			},
		}}, nil
	case *message.UpstreamOpenRequest:
		qos, err := toQoSProto(msg.QoS)
		if err != nil {
			return nil, errorConvertToProto(msg, err)
		}
		return &autogen.Message{Message: &autogen.Message_UpstreamOpenRequest{
			UpstreamOpenRequest: &autogen.UpstreamOpenRequest{
				RequestId:       uint32(msg.RequestID),
				SessionId:       msg.SessionID,
				AckInterval:     uint32(msg.AckInterval.Milliseconds()),
				ExpiryInterval:  uint32(msg.ExpiryInterval.Seconds()),
				DataIds:         toDataIDsProto(msg.DataIDs),
				Qos:             qos,
				ExtensionFields: toUpstreamOpenRequestExtensionFieldsProto(msg.ExtensionFields),
			},
		}}, nil
	case *message.UpstreamOpenResponse:
		rc, err := toResultCodeProto(msg.ResultCode)
		if err != nil {
			return nil, errorConvertToProto(msg, err)
		}
		return &autogen.Message{Message: &autogen.Message_UpstreamOpenResponse{
			UpstreamOpenResponse: &autogen.UpstreamOpenResponse{
				RequestId:             uint32(msg.RequestID),
				AssignedStreamId:      msg.AssignedStreamID[:],
				AssignedStreamIdAlias: msg.AssignedStreamIDAlias,
				DataIdAliases:         toDataIDAliasesProto(msg.DataIDAliases),
				ServerTime:            msg.ServerTimeOrUnixZero(),
				ResultCode:            rc,
				ResultString:          msg.ResultString,
				ExtensionFields:       toUpstreamOpenResponseExtensionFieldsProto(msg.ExtensionFields),
			},
		}}, nil
	case *message.UpstreamResumeRequest:
		return &autogen.Message{Message: &autogen.Message_UpstreamResumeRequest{
			UpstreamResumeRequest: &autogen.UpstreamResumeRequest{
				RequestId:       uint32(msg.RequestID),
				StreamId:        msg.StreamID[:],
				ExtensionFields: toUpstreamResumeRequestExtensionFieldsProto(msg.ExtensionFields),
			},
		}}, nil
	case *message.UpstreamResumeResponse:
		rc, err := toResultCodeProto(msg.ResultCode)
		if err != nil {
			return nil, errorConvertToProto(msg, err)
		}
		return &autogen.Message{Message: &autogen.Message_UpstreamResumeResponse{
			UpstreamResumeResponse: &autogen.UpstreamResumeResponse{
				RequestId:             uint32(msg.RequestID),
				AssignedStreamIdAlias: msg.AssignedStreamIDAlias,
				ResultCode:            rc,
				ResultString:          msg.ResultString,
				ExtensionFields:       toUpstreamResumeResponseExtensionFieldsProto(msg.ExtensionFields),
			},
		}}, nil
	case *message.UpstreamCloseRequest:
		return &autogen.Message{Message: &autogen.Message_UpstreamCloseRequest{
			UpstreamCloseRequest: &autogen.UpstreamCloseRequest{
				RequestId:           uint32(msg.RequestID),
				StreamId:            msg.StreamID[:],
				TotalDataPoints:     msg.TotalDataPoints,
				FinalSequenceNumber: msg.FinalSequenceNumber,
				ExtensionFields:     toUpstreamCloseRequestExtensionFieldsProto(msg.ExtensionFields),
			},
		}}, nil
	case *message.UpstreamCloseResponse:
		rc, err := toResultCodeProto(msg.ResultCode)
		if err != nil {
			return nil, errorConvertToProto(msg, err)
		}
		return &autogen.Message{Message: &autogen.Message_UpstreamCloseResponse{
			UpstreamCloseResponse: &autogen.UpstreamCloseResponse{
				RequestId:       uint32(msg.RequestID),
				ResultCode:      rc,
				ResultString:    msg.ResultString,
				ExtensionFields: toUpstreamCloseResponseExtensionFieldsProto(msg.ExtensionFields),
			},
		}}, nil
	case *message.DownstreamOpenRequest:
		qos, err := toQoSProto(msg.QoS)
		if err != nil {
			return nil, errorConvertToProto(msg, err)
		}
		return &autogen.Message{Message: &autogen.Message_DownstreamOpenRequest{
			DownstreamOpenRequest: &autogen.DownstreamOpenRequest{
				RequestId:            uint32(msg.RequestID),
				DesiredStreamIdAlias: msg.DesiredStreamIDAlias,
				DownstreamFilters:    toDownstreamFiltersProto(msg.DownstreamFilters),
				ExpiryInterval:       uint32(msg.ExpiryInterval.Seconds()),
				DataIdAliases:        toDataIDAliasesProto(msg.DataIDAliases),
				Qos:                  qos,
				ExtensionFields:      toDownstreamOpenRequestExtensionFieldsProto(msg.ExtensionFields),
				OmitEmptyChunk:       msg.OmitEmptyChunk,
			},
		}}, nil
	case *message.DownstreamOpenResponse:
		rc, err := toResultCodeProto(msg.ResultCode)
		if err != nil {
			return nil, errorConvertToProto(msg, err)
		}
		return &autogen.Message{Message: &autogen.Message_DownstreamOpenResponse{
			DownstreamOpenResponse: &autogen.DownstreamOpenResponse{
				RequestId:        uint32(msg.RequestID),
				AssignedStreamId: msg.AssignedStreamID[:],
				ServerTime:       msg.ServerTimeOrUnixZero(),
				ResultCode:       rc,
				ResultString:     msg.ResultString,
				ExtensionFields:  toDownstreamOpenResponseExtensionFieldsProto(msg.ExtensionFields),
			},
		}}, nil
	case *message.DownstreamResumeRequest:
		return &autogen.Message{Message: &autogen.Message_DownstreamResumeRequest{
			DownstreamResumeRequest: &autogen.DownstreamResumeRequest{
				RequestId:            uint32(msg.RequestID),
				StreamId:             msg.StreamID[:],
				DesiredStreamIdAlias: msg.DesiredStreamIDAlias,
				ExtensionFields:      toDownstreamResumeRequestExtensionFieldsProto(msg.ExtensionFields),
			},
		}}, nil
	case *message.DownstreamResumeResponse:
		rc, err := toResultCodeProto(msg.ResultCode)
		if err != nil {
			return nil, errorConvertToProto(msg, err)
		}
		return &autogen.Message{Message: &autogen.Message_DownstreamResumeResponse{
			DownstreamResumeResponse: &autogen.DownstreamResumeResponse{
				RequestId:       uint32(msg.RequestID),
				ResultCode:      rc,
				ResultString:    msg.ResultString,
				ExtensionFields: toDownstreamResumeResponseExtensionFieldsProto(msg.ExtensionFields),
			},
		}}, nil
	case *message.DownstreamCloseRequest:
		return &autogen.Message{Message: &autogen.Message_DownstreamCloseRequest{
			DownstreamCloseRequest: &autogen.DownstreamCloseRequest{
				RequestId:       uint32(msg.RequestID),
				StreamId:        msg.StreamID[:],
				ExtensionFields: toDownstreamCloseRequestExtensionFieldsProto(msg.ExtensionFields),
			},
		}}, nil
	case *message.DownstreamCloseResponse:
		rc, err := toResultCodeProto(msg.ResultCode)
		if err != nil {
			return nil, errorConvertToProto(msg, err)
		}
		return &autogen.Message{Message: &autogen.Message_DownstreamCloseResponse{
			DownstreamCloseResponse: &autogen.DownstreamCloseResponse{
				RequestId:       uint32(msg.RequestID),
				ResultCode:      rc,
				ResultString:    msg.ResultString,
				ExtensionFields: toDownstreamCloseResponseExtensionFieldsProto(msg.ExtensionFields),
			},
		}}, nil
	case *message.UpstreamCall:
		return &autogen.Message{Message: &autogen.Message_UpstreamCall{
			UpstreamCall: &autogen.UpstreamCall{
				CallId:            msg.CallID,
				RequestCallId:     msg.RequestCallID,
				DestinationNodeId: msg.DestinationNodeID,
				Name:              msg.Name,
				Type:              msg.Type,
				Payload:           msg.Payload,
				ExtensionFields:   toUpstreamCallExtensionFieldsProto(msg.ExtensionFields),
			},
		}}, nil

	case *message.UpstreamCallAck:
		rc, err := toResultCodeProto(msg.ResultCode)
		if err != nil {
			return nil, errorConvertToProto(msg, err)
		}
		return &autogen.Message{Message: &autogen.Message_UpstreamCallAck{
			UpstreamCallAck: &autogen.UpstreamCallAck{
				CallId:          msg.CallID,
				ResultCode:      rc,
				ResultString:    msg.ResultString,
				ExtensionFields: toUpstreamCallAckExtensionFieldsProto(msg.ExtensionFields),
			},
		}}, nil
	case *message.DownstreamCall:
		return &autogen.Message{Message: &autogen.Message_DownstreamCall{
			DownstreamCall: &autogen.DownstreamCall{
				CallId:          msg.CallID,
				RequestCallId:   msg.RequestCallID,
				SourceNodeId:    msg.SourceNodeID,
				Name:            msg.Name,
				Type:            msg.Type,
				Payload:         msg.Payload,
				ExtensionFields: toDownstreamCallExtensionFieldsProto(msg.ExtensionFields),
			},
		}}, nil
	case *message.Ping:
		return &autogen.Message{Message: &autogen.Message_Ping{
			Ping: &autogen.Ping{
				RequestId:       uint32(msg.RequestID),
				ExtensionFields: toPingExtensionFieldsProto(msg.ExtensionFields),
			},
		}}, nil
	case *message.Pong:
		return &autogen.Message{Message: &autogen.Message_Pong{
			Pong: &autogen.Pong{
				RequestId:       uint32(msg.RequestID),
				ExtensionFields: toPongExtensionFieldsProto(msg.ExtensionFields),
			},
		}}, nil

	// ストリームメッセージ
	case *message.UpstreamChunk:
		chunk, err := toStreamChunkProto(msg.StreamChunk)
		if err != nil {
			return nil, errorConvertToProto(msg, err)
		}
		return &autogen.Message{Message: &autogen.Message_UpstreamChunk{
			UpstreamChunk: &autogen.UpstreamChunk{
				StreamIdAlias:   msg.StreamIDAlias,
				StreamChunk:     chunk,
				DataIds:         toDataIDsProto(msg.DataIDs),
				ExtensionFields: toUpstreamChunkExtensionFieldsProto(msg.ExtensionFields),
			},
		}}, nil
	case *message.UpstreamChunkAck:
		dprs, err := toUpstreamDataPointResultsProto(msg.Results)
		if err != nil {
			return nil, errorConvertToProto(msg, err)
		}
		return &autogen.Message{Message: &autogen.Message_UpstreamChunkAck{
			UpstreamChunkAck: &autogen.UpstreamChunkAck{
				StreamIdAlias:   msg.StreamIDAlias,
				Results:         dprs,
				DataIdAliases:   toDataIDAliasesProto(msg.DataIDAliases),
				ExtensionFields: toUpstreamChunkAckExtensionFieldsProto(msg.ExtensionFields),
			},
		}}, nil
	case *message.DownstreamChunk:
		chunk, err := toDownstreamChunkProto(msg)
		if err != nil {
			return nil, errorConvertToProto(msg, err)
		}
		return &autogen.Message{Message: &autogen.Message_DownstreamChunk{
			DownstreamChunk: chunk,
		}}, nil
	case *message.DownstreamChunkAck:
		dprs, err := toDownstreamDataPointResultsProto(msg.Results)
		if err != nil {
			return nil, errorConvertToProto(msg, err)
		}
		return &autogen.Message{Message: &autogen.Message_DownstreamChunkAck{
			DownstreamChunkAck: &autogen.DownstreamChunkAck{
				StreamIdAlias:   msg.StreamIDAlias,
				AckId:           msg.AckID,
				Results:         dprs,
				UpstreamAliases: toUpstreamAliasesProto(msg.UpstreamAliases),
				DataIdAliases:   toDataIDAliasesProto(msg.DataIDAliases),
				ExtensionFields: toDownstreamChunkAckExtensionFieldsProto(msg.ExtensionFields),
			},
		}}, nil
	case *message.DownstreamChunkAckComplete:
		rc, err := toResultCodeProto(msg.ResultCode)
		if err != nil {
			return nil, errorConvertToProto(msg, err)
		}
		return &autogen.Message{Message: &autogen.Message_DownstreamChunkAckComplete{
			DownstreamChunkAckComplete: &autogen.DownstreamChunkAckComplete{
				StreamIdAlias:   msg.StreamIDAlias,
				AckId:           msg.AckID,
				ResultCode:      rc,
				ResultString:    msg.ResultString,
				ExtensionFields: toDownstreamChunkAckCompleteExtensionFieldsProto(msg.ExtensionFields),
			},
		}}, nil
	case *message.UpstreamMetadata:
		meta, err := toUpstreamMetadataProto(msg)
		if err != nil {
			return nil, errorConvertToProto(msg, err)
		}
		return &autogen.Message{Message: &autogen.Message_UpstreamMetadata{
			UpstreamMetadata: meta,
		}}, nil
	case *message.UpstreamMetadataAck:
		rc, err := toResultCodeProto(msg.ResultCode)
		if err != nil {
			return nil, errorConvertToProto(msg, err)
		}
		return &autogen.Message{Message: &autogen.Message_UpstreamMetadataAck{
			UpstreamMetadataAck: &autogen.UpstreamMetadataAck{
				RequestId:       uint32(msg.RequestID),
				ResultCode:      rc,
				ResultString:    msg.ResultString,
				ExtensionFields: toUpstreamMetadataAckExtensionFieldsProto(msg.ExtensionFields),
			},
		}}, nil
	case *message.DownstreamMetadata:
		dmeta, err := toDownstreamMetadataProto(msg)
		if err != nil {
			return nil, errorConvertToProto(msg, err)
		}
		return &autogen.Message{Message: &autogen.Message_DownstreamMetadata{
			DownstreamMetadata: dmeta,
		}}, nil
	case *message.DownstreamMetadataAck:
		rc, err := toResultCodeProto(msg.ResultCode)
		if err != nil {
			return nil, errorConvertToProto(msg, err)
		}
		return &autogen.Message{Message: &autogen.Message_DownstreamMetadataAck{
			DownstreamMetadataAck: &autogen.DownstreamMetadataAck{
				RequestId:       uint32(msg.RequestID),
				ResultCode:      rc,
				ResultString:    msg.ResultString,
				ExtensionFields: toDownstreamMetadataAckExtensionFieldsProto(msg.ExtensionFields),
			},
		}}, nil
	default:
		return nil, errors.Errorf("msg type: %T : %+v : %w", msg, msg, errors.ErrMalformedMessage)
	}
}

func toResultCodeProto(in message.ResultCode) (autogen.ResultCode, error) {
	switch in {
	case message.ResultCodeSucceeded, message.ResultCodeNormalClosure:
		return autogen.ResultCode_SUCCEEDED, nil
	case message.ResultCodeIncompatibleVersion:
		return autogen.ResultCode_INCOMPATIBLE_VERSION, nil
	case message.ResultCodeMaximumDataIDAlias:
		return autogen.ResultCode_MAXIMUM_DATA_ID_ALIAS, nil
	case message.ResultCodeMaximumUpstreamAlias:
		return autogen.ResultCode_MAXIMUM_UPSTREAM_ALIAS, nil
	case message.ResultCodeUnspecifiedError:
		return autogen.ResultCode_UNSPECIFIED_ERROR, nil
	case message.ResultCodeNoNodeID:
		return autogen.ResultCode_NO_NODE_ID, nil
	case message.ResultCodeAuthFailed:
		return autogen.ResultCode_AUTH_FAILED, nil
	case message.ResultCodeConnectTimeout:
		return autogen.ResultCode_CONNECT_TIMEOUT, nil
	case message.ResultCodeMalformedMessage:
		return autogen.ResultCode_MALFORMED_MESSAGE, nil
	case message.ResultCodeProtocolError:
		return autogen.ResultCode_PROTOCOL_ERROR, nil
	case message.ResultCodeAckTimeout:
		return autogen.ResultCode_ACK_TIMEOUT, nil
	case message.ResultCodeInvalidPayload:
		return autogen.ResultCode_INVALID_PAYLOAD, nil
	case message.ResultCodeInvalidDataID:
		return autogen.ResultCode_INVALID_DATA_ID, nil
	case message.ResultCodeInvalidDataIDAlias:
		return autogen.ResultCode_INVALID_DATA_ID_ALIAS, nil
	case message.ResultCodeInvalidDataFilter:
		return autogen.ResultCode_INVALID_DATA_FILTER, nil
	case message.ResultCodeStreamNotFound:
		return autogen.ResultCode_STREAM_NOT_FOUND, nil
	case message.ResultCodeResumeRequestConflict:
		return autogen.ResultCode_RESUME_REQUEST_CONFLICT, nil
	case message.ResultCodeProcessFailed:
		return autogen.ResultCode_PROCESS_FAILED, nil
	case message.ResultCodeDesiredQosNotSupported:
		return autogen.ResultCode_DESIRED_QOS_NOT_SUPPORTED, nil
	case message.ResultCodePingTimeout:
		return autogen.ResultCode_PING_TIMEOUT, nil
	case message.ResultCodeTooLargeMessageSize:
		return autogen.ResultCode_TOO_LARGE_MESSAGE_SIZE, nil
	case message.ResultCodeTooManyDataIDAliases:
		return autogen.ResultCode_TOO_MANY_DATA_ID_ALIASES, nil
	case message.ResultCodeTooManyStreams:
		return autogen.ResultCode_TOO_MANY_STREAMS, nil
	case message.ResultCodeTooLongAckInterval:
		return autogen.ResultCode_TOO_LONG_ACK_INTERVAL, nil
	case message.ResultCodeTooManyDownstreamFilters:
		return autogen.ResultCode_TOO_MANY_DOWNSTREAM_FILTERS, nil
	case message.ResultCodeTooManyDataFilters:
		return autogen.ResultCode_TOO_MANY_DATA_FILTERS, nil
	case message.ResultCodeTooLongExpiryInterval:
		return autogen.ResultCode_TOO_LONG_EXPIRY_INTERVAL, nil
	case message.ResultCodeTooLongPingTimeout:
		return autogen.ResultCode_TOO_LONG_PING_TIMEOUT, nil
	case message.ResultCodeTooShortPingTimeout:
		return autogen.ResultCode_TOO_SHORT_PING_TIMEOUT, nil
	case message.ResultCodeNodeIDMismatch:
		return autogen.ResultCode_NODE_ID_MISMATCH, nil
	case message.ResultCodeRateLimitReached:
		return autogen.ResultCode_RATE_LIMIT_REACHED, nil
	case message.ResultCodeSessionNotFound:
		return autogen.ResultCode_SESSION_NOT_FOUND, nil
	case message.ResultCodeSessionAlreadyClosed:
		return autogen.ResultCode_SESSION_ALREADY_CLOSED, nil
	case message.ResultCodeSessionCannotClosed:
		return autogen.ResultCode_SESSION_CANNOT_CLOSED, nil
	}
	return 0, errors.Errorf("result_code:%v : %w", in, errors.ErrMalformedMessage)
}

func toDataIDAliasesProto(in map[uint32]*message.DataID) map[uint32]*autogen.DataID {
	res := make(map[uint32]*autogen.DataID, len(in))
	for k, v := range in {
		res[k] = &autogen.DataID{
			Name: v.Name,
			Type: v.Type,
		}
	}
	return res
}

func toDataIDsProto(in []*message.DataID) []*autogen.DataID {
	res := make([]*autogen.DataID, 0, len(in))
	for _, v := range in {
		res = append(res, &autogen.DataID{
			Name: v.Name,
			Type: v.Type,
		})
	}
	return res
}

func toQoSProto(in message.QoS) (autogen.QoS, error) {
	switch in {
	case message.QoSReliable:
		return autogen.QoS_RELIABLE, nil
	case message.QoSUnreliable:
		return autogen.QoS_UNRELIABLE, nil
	case message.QoSPartial:
		return autogen.QoS_PARTIAL, nil
	}
	return 0, errors.Errorf("qos:%v : %w", in, errors.ErrMalformedMessage)
}

func toDataPointsProto(in []*message.DataPoint) []*autogen.DataPoint {
	res := make([]*autogen.DataPoint, 0, len(in))
	for _, v := range in {
		res = append(res, &autogen.DataPoint{
			ElapsedTime: int64(v.ElapsedTime),
			Payload:     v.Payload,
		})
	}
	return res
}

func toDownstreamFiltersProto(in []*message.DownstreamFilter) []*autogen.DownstreamFilter {
	res := make([]*autogen.DownstreamFilter, 0, len(in))
	for _, v := range in {
		res = append(res, &autogen.DownstreamFilter{
			SourceNodeId: v.SourceNodeID,
			DataFilters:  toDataFiltersProto(v.DataFilters),
		})
	}
	return res
}

func toDataFiltersProto(in []*message.DataFilter) []*autogen.DataFilter {
	res := make([]*autogen.DataFilter, 0, len(in))
	for _, v := range in {
		res = append(res, &autogen.DataFilter{
			Name: v.Name,
			Type: v.Type,
		})
	}
	return res
}

func toUpstreamDataPointResultsProto(in []*message.UpstreamChunkResult) ([]*autogen.UpstreamChunkResult, error) {
	res := make([]*autogen.UpstreamChunkResult, 0, len(in))
	for _, v := range in {
		rs, err := toResultCodeProto(v.ResultCode)
		if err != nil {
			return nil, err
		}
		res = append(res, &autogen.UpstreamChunkResult{
			SequenceNumber:  v.SequenceNumber,
			ResultCode:      rs,
			ResultString:    v.ResultString,
			ExtensionFields: toUpstreamChunkResultExtensionFieldsProto(v.ExtensionFields),
		})
	}
	return res, nil
}

func toDownstreamDataPointResultsProto(in []*message.DownstreamChunkResult) ([]*autogen.DownstreamChunkResult, error) {
	res := make([]*autogen.DownstreamChunkResult, 0, len(in))
	for _, v := range in {
		rs, err := toResultCodeProto(v.ResultCode)
		if err != nil {
			return nil, err
		}
		res = append(res, &autogen.DownstreamChunkResult{
			StreamIdOfUpstream:       v.StreamIDOfUpstream[:],
			SequenceNumberInUpstream: v.SequenceNumberInUpstream,
			ResultCode:               rs,
			ResultString:             v.ResultString,
			ExtensionFields:          toDownstreamChunkResultExtensionFieldsProto(v.ExtensionFields),
		})
	}
	return res, nil
}

func toDownstreamChunkProto(in *message.DownstreamChunk) (*autogen.DownstreamChunk, error) {
	if in == nil {
		return &autogen.DownstreamChunk{}, nil
	}
	chunk, err := toStreamChunkProto(in.StreamChunk)
	if err != nil {
		return nil, err
	}
	res := &autogen.DownstreamChunk{
		StreamIdAlias:              in.StreamIDAlias,
		UpstreamOrAlias:            nil,
		StreamChunk:                chunk,
		ExtensionFields:            toDownstreamChunkExtensionFieldsProto(in.ExtensionFields),
		DownstreamFilterReferences: toDownstreamFilterReferencesProto(in.DownstreamFilterReferences),
	}

	switch v := in.UpstreamOrAlias.(type) {
	case *message.UpstreamInfo:
		res.UpstreamOrAlias = &autogen.DownstreamChunk_UpstreamInfo{
			UpstreamInfo: &autogen.UpstreamInfo{
				SessionId:    v.SessionID,
				StreamId:     v.StreamID[:],
				SourceNodeId: v.SourceNodeID,
			},
		}
	case message.UpstreamAlias:
		res.UpstreamOrAlias = &autogen.DownstreamChunk_UpstreamAlias{
			UpstreamAlias: uint32(v),
		}
	default:
		return nil, errors.Errorf("upstream_or_alias:%v : %w", in, errors.ErrMalformedMessage)
	}
	return res, nil
}

func toUpstreamMetadataProto(in *message.UpstreamMetadata) (*autogen.UpstreamMetadata, error) {
	if in == nil {
		return &autogen.UpstreamMetadata{}, nil
	}
	res := &autogen.UpstreamMetadata{
		RequestId:       uint32(in.RequestID),
		Metadata:        nil,
		ExtensionFields: toUpstreamMetadataExtensionFieldsProto(in.ExtensionFields),
	}
	switch v := in.Metadata.(type) {
	case *message.BaseTime:
		res.Metadata = &autogen.UpstreamMetadata_BaseTime{
			BaseTime: ToBaseTimeProto(v),
		}
	default:
		return nil, errors.Errorf("upstream_metadata:%v : %w", in.Metadata, errors.ErrMalformedMessage)
	}
	return res, nil
}

func ToBaseTimeProto(v *message.BaseTime) *autogen.BaseTime {
	return &autogen.BaseTime{
		SessionId:   v.SessionID,
		Name:        v.Name,
		Priority:    uint32(v.Priority),
		ElapsedTime: uint64(v.ElapsedTime),
		BaseTime:    v.BaseTime.UnixNano(),
	}
}

func toDownstreamMetadataProto(in *message.DownstreamMetadata) (*autogen.DownstreamMetadata, error) {
	res := &autogen.DownstreamMetadata{
		RequestId:       uint32(in.RequestID),
		StreamIdAlias:   in.StreamIDAlias,
		SourceNodeId:    in.SourceNodeID,
		Metadata:        nil,
		ExtensionFields: toDownstreamMetadataExtensionFieldsProto(in.ExtensionFields),
	}
	switch v := in.Metadata.(type) {
	case *message.BaseTime:
		res.Metadata = &autogen.DownstreamMetadata_BaseTime{
			BaseTime: ToBaseTimeProto(v),
		}
	case *message.UpstreamOpen:
		meta, err := ToUpstreamOpenProto(v)
		if err != nil {
			return nil, err
		}
		res.Metadata = &autogen.DownstreamMetadata_UpstreamOpen{
			UpstreamOpen: meta,
		}
	case *message.UpstreamAbnormalClose:
		meta := ToUpstreamAbnormalCloseProto(v)
		res.Metadata = &autogen.DownstreamMetadata_UpstreamAbnormalClose{
			UpstreamAbnormalClose: meta,
		}
	case *message.UpstreamResume:
		meta, err := ToUpstreamResumeProto(v)
		if err != nil {
			return nil, err
		}
		res.Metadata = &autogen.DownstreamMetadata_UpstreamResume{
			UpstreamResume: meta,
		}
	case *message.UpstreamNormalClose:
		meta := ToUpstreamNormalCloseProto(v)
		res.Metadata = &autogen.DownstreamMetadata_UpstreamNormalClose{
			UpstreamNormalClose: meta,
		}

	case *message.DownstreamOpen:
		meta, err := ToDownstreamOpenProto(v)
		if err != nil {
			return nil, err
		}
		res.Metadata = &autogen.DownstreamMetadata_DownstreamOpen{
			DownstreamOpen: meta,
		}

	case *message.DownstreamAbnormalClose:
		meta := ToDownstreamAbnormalCloseProto(v)
		res.Metadata = &autogen.DownstreamMetadata_DownstreamAbnormalClose{
			DownstreamAbnormalClose: meta,
		}
	case *message.DownstreamResume:
		meta, err := ToDownstreamResumeProto(v)
		if err != nil {
			return nil, err
		}
		res.Metadata = &autogen.DownstreamMetadata_DownstreamResume{
			DownstreamResume: meta,
		}
	case *message.DownstreamNormalClose:
		meta := ToDownstreamNormalCloseProto(v)
		res.Metadata = &autogen.DownstreamMetadata_DownstreamNormalClose{
			DownstreamNormalClose: meta,
		}
	default:
		return nil, errors.Errorf("downstream_metadata:%v : %w", in.Metadata, errors.ErrMalformedMessage)
	}
	return res, nil
}

func ToUpstreamOpenProto(v *message.UpstreamOpen) (*autogen.UpstreamOpen, error) {
	qos, err := toQoSProto(v.QoS)
	if err != nil {
		return nil, err
	}
	return &autogen.UpstreamOpen{
		StreamId:  v.StreamID[:],
		SessionId: v.SessionID,
		Qos:       qos,
	}, nil
}

func ToUpstreamAbnormalCloseProto(v *message.UpstreamAbnormalClose) *autogen.UpstreamAbnormalClose {
	return &autogen.UpstreamAbnormalClose{
		StreamId:  v.StreamID[:],
		SessionId: v.SessionID,
	}
}

func ToUpstreamResumeProto(v *message.UpstreamResume) (*autogen.UpstreamResume, error) {
	qos, err := toQoSProto(v.QoS)
	if err != nil {
		return nil, err
	}
	return &autogen.UpstreamResume{
		StreamId:  v.StreamID[:],
		SessionId: v.SessionID,
		Qos:       qos,
	}, nil
}

func ToUpstreamNormalCloseProto(v *message.UpstreamNormalClose) *autogen.UpstreamNormalClose {
	return &autogen.UpstreamNormalClose{
		StreamId:            v.StreamID[:],
		SessionId:           v.SessionID,
		TotalDataPoints:     v.TotalDataPoints,
		FinalSequenceNumber: v.FinalSequenceNumber,
	}
}

func ToDownstreamOpenProto(v *message.DownstreamOpen) (*autogen.DownstreamOpen, error) {
	qos, err := toQoSProto(v.QoS)
	if err != nil {
		return nil, err
	}
	return &autogen.DownstreamOpen{
		StreamId:          v.StreamID[:],
		DownstreamFilters: toDownstreamFiltersProto(v.DownstreamFilters),
		Qos:               qos,
	}, nil
}

func ToDownstreamAbnormalCloseProto(v *message.DownstreamAbnormalClose) *autogen.DownstreamAbnormalClose {
	return &autogen.DownstreamAbnormalClose{
		StreamId: v.StreamID[:],
	}
}

func ToDownstreamResumeProto(v *message.DownstreamResume) (*autogen.DownstreamResume, error) {
	qos, err := toQoSProto(v.QoS)
	if err != nil {
		return nil, err
	}
	return &autogen.DownstreamResume{
		StreamId:          v.StreamID[:],
		DownstreamFilters: toDownstreamFiltersProto(v.DownstreamFilters),
		Qos:               qos,
	}, nil
}

func ToDownstreamNormalCloseProto(v *message.DownstreamNormalClose) *autogen.DownstreamNormalClose {
	return &autogen.DownstreamNormalClose{
		StreamId: v.StreamID[:],
	}
}

func toConnectRequestExtensionFieldsProto(in *message.ConnectRequestExtensionFields) *autogenextensions.ConnectRequestExtensionFields {
	if in == nil {
		return nil
	}
	return &autogenextensions.ConnectRequestExtensionFields{
		AccessToken: in.AccessToken,
		Intdash:     toIntdashExtenstionFieldsProto(in.Intdash),
	}
}

func toIntdashExtenstionFieldsProto(in *message.IntdashExtensionFields) *autogenextensions.IntdashExtensionFields {
	if in == nil {
		return nil
	}
	return &autogenextensions.IntdashExtensionFields{
		ProjectUuid: in.ProjectUUID.String(),
	}
}

func toConnectResponseExtensionFieldsProto(in *message.ConnectResponseExtensionFields) *autogenextensions.ConnectResponseExtensionFields {
	if in == nil {
		return nil
	}
	return &autogenextensions.ConnectResponseExtensionFields{}
}

func toDisconnectExtensionFieldsProto(in *message.DisconnectExtensionFields) *autogenextensions.DisconnectExtensionFields {
	if in == nil {
		return nil
	}
	return &autogenextensions.DisconnectExtensionFields{}
}

func toUpstreamOpenRequestExtensionFieldsProto(in *message.UpstreamOpenRequestExtensionFields) *autogenextensions.UpstreamOpenRequestExtensionFields {
	if in == nil {
		return nil
	}
	return &autogenextensions.UpstreamOpenRequestExtensionFields{
		Persist: in.Persist,
	}
}

func toUpstreamOpenResponseExtensionFieldsProto(in *message.UpstreamOpenResponseExtensionFields) *autogenextensions.UpstreamOpenResponseExtensionFields {
	if in == nil {
		return nil
	}
	return &autogenextensions.UpstreamOpenResponseExtensionFields{}
}

func toUpstreamResumeRequestExtensionFieldsProto(in *message.UpstreamResumeRequestExtensionFields) *autogenextensions.UpstreamResumeRequestExtensionFields {
	if in == nil {
		return nil
	}
	return &autogenextensions.UpstreamResumeRequestExtensionFields{}
}

func toUpstreamResumeResponseExtensionFieldsProto(in *message.UpstreamResumeResponseExtensionFields) *autogenextensions.UpstreamResumeResponseExtensionFields {
	if in == nil {
		return nil
	}
	return &autogenextensions.UpstreamResumeResponseExtensionFields{}
}

func toUpstreamCloseRequestExtensionFieldsProto(in *message.UpstreamCloseRequestExtensionFields) *autogenextensions.UpstreamCloseRequestExtensionFields {
	if in == nil {
		return nil
	}
	return &autogenextensions.UpstreamCloseRequestExtensionFields{
		CloseSession: in.CloseSession,
	}
}

func toUpstreamCloseResponseExtensionFieldsProto(in *message.UpstreamCloseResponseExtensionFields) *autogenextensions.UpstreamCloseResponseExtensionFields {
	if in == nil {
		return nil
	}
	return &autogenextensions.UpstreamCloseResponseExtensionFields{}
}

func toDownstreamOpenRequestExtensionFieldsProto(in *message.DownstreamOpenRequestExtensionFields) *autogenextensions.DownstreamOpenRequestExtensionFields {
	if in == nil {
		return nil
	}
	return &autogenextensions.DownstreamOpenRequestExtensionFields{}
}

func toDownstreamOpenResponseExtensionFieldsProto(in *message.DownstreamOpenResponseExtensionFields) *autogenextensions.DownstreamOpenResponseExtensionFields {
	if in == nil {
		return nil
	}
	return &autogenextensions.DownstreamOpenResponseExtensionFields{}
}

func toDownstreamResumeRequestExtensionFieldsProto(in *message.DownstreamResumeRequestExtensionFields) *autogenextensions.DownstreamResumeRequestExtensionFields {
	if in == nil {
		return nil
	}
	return &autogenextensions.DownstreamResumeRequestExtensionFields{}
}

func toDownstreamResumeResponseExtensionFieldsProto(in *message.DownstreamResumeResponseExtensionFields) *autogenextensions.DownstreamResumeResponseExtensionFields {
	if in == nil {
		return nil
	}
	return &autogenextensions.DownstreamResumeResponseExtensionFields{}
}

func toDownstreamCloseRequestExtensionFieldsProto(in *message.DownstreamCloseRequestExtensionFields) *autogenextensions.DownstreamCloseRequestExtensionFields {
	if in == nil {
		return nil
	}
	return &autogenextensions.DownstreamCloseRequestExtensionFields{}
}

func toDownstreamCloseResponseExtensionFieldsProto(in *message.DownstreamCloseResponseExtensionFields) *autogenextensions.DownstreamCloseResponseExtensionFields {
	if in == nil {
		return nil
	}
	return &autogenextensions.DownstreamCloseResponseExtensionFields{}
}

func toUpstreamCallExtensionFieldsProto(in *message.UpstreamCallExtensionFields) *autogenextensions.UpstreamCallExtensionFields {
	if in == nil {
		return nil
	}
	return &autogenextensions.UpstreamCallExtensionFields{}
}

func toUpstreamCallAckExtensionFieldsProto(in *message.UpstreamCallAckExtensionFields) *autogenextensions.UpstreamCallAckExtensionFields {
	if in == nil {
		return nil
	}
	return &autogenextensions.UpstreamCallAckExtensionFields{}
}

func toDownstreamCallExtensionFieldsProto(in *message.DownstreamCallExtensionFields) *autogenextensions.DownstreamCallExtensionFields {
	if in == nil {
		return nil
	}
	return &autogenextensions.DownstreamCallExtensionFields{}
}

func toPingExtensionFieldsProto(in *message.PingExtensionFields) *autogenextensions.PingExtensionFields {
	if in == nil {
		return nil
	}
	return &autogenextensions.PingExtensionFields{}
}

func toPongExtensionFieldsProto(in *message.PongExtensionFields) *autogenextensions.PongExtensionFields {
	if in == nil {
		return nil
	}
	return &autogenextensions.PongExtensionFields{}
}

func toUpstreamChunkExtensionFieldsProto(in *message.UpstreamChunkExtensionFields) *autogenextensions.UpstreamChunkExtensionFields {
	if in == nil {
		return nil
	}
	return &autogenextensions.UpstreamChunkExtensionFields{}
}

func toUpstreamChunkAckExtensionFieldsProto(in *message.UpstreamChunkAckExtensionFields) *autogenextensions.UpstreamChunkAckExtensionFields {
	if in == nil {
		return nil
	}
	return &autogenextensions.UpstreamChunkAckExtensionFields{}
}

func toDownstreamChunkAckExtensionFieldsProto(in *message.DownstreamChunkAckExtensionFields) *autogenextensions.DownstreamChunkAckExtensionFields {
	if in == nil {
		return nil
	}
	return &autogenextensions.DownstreamChunkAckExtensionFields{}
}

func toUpstreamMetadataAckExtensionFieldsProto(in *message.UpstreamMetadataAckExtensionFields) *autogenextensions.UpstreamMetadataAckExtensionFields {
	if in == nil {
		return nil
	}
	return &autogenextensions.UpstreamMetadataAckExtensionFields{}
}

func toDownstreamMetadataAckExtensionFieldsProto(in *message.DownstreamMetadataAckExtensionFields) *autogenextensions.DownstreamMetadataAckExtensionFields {
	if in == nil {
		return nil
	}
	return &autogenextensions.DownstreamMetadataAckExtensionFields{}
}

func toUpstreamChunkResultExtensionFieldsProto(in *message.UpstreamChunkResultExtensionFields) *autogenextensions.UpstreamChunkResultExtensionFields {
	if in == nil {
		return nil
	}
	return &autogenextensions.UpstreamChunkResultExtensionFields{}
}

func toDownstreamChunkResultExtensionFieldsProto(in *message.DownstreamChunkResultExtensionFields) *autogenextensions.DownstreamChunkResultExtensionFields {
	if in == nil {
		return nil
	}
	return &autogenextensions.DownstreamChunkResultExtensionFields{}
}

func toDownstreamChunkExtensionFieldsProto(in *message.DownstreamChunkExtensionFields) *autogenextensions.DownstreamChunkExtensionFields {
	if in == nil {
		return nil
	}
	return &autogenextensions.DownstreamChunkExtensionFields{}
}

func toUpstreamMetadataExtensionFieldsProto(in *message.UpstreamMetadataExtensionFields) *autogenextensions.UpstreamMetadataExtensionFields {
	if in == nil {
		return nil
	}
	return &autogenextensions.UpstreamMetadataExtensionFields{
		Persist: in.Persist,
	}
}

func toDownstreamMetadataExtensionFieldsProto(in *message.DownstreamMetadataExtensionFields) *autogenextensions.DownstreamMetadataExtensionFields {
	if in == nil {
		return nil
	}
	return &autogenextensions.DownstreamMetadataExtensionFields{}
}

func toDownstreamChunkAckCompleteExtensionFieldsProto(in *message.DownstreamChunkAckCompleteExtensionFields) *autogenextensions.DownstreamChunkAckCompleteExtensionFields {
	if in == nil {
		return nil
	}
	return &autogenextensions.DownstreamChunkAckCompleteExtensionFields{}
}

func toUpstreamAliasesProto(in map[uint32]*message.UpstreamInfo) map[uint32]*autogen.UpstreamInfo {
	res := make(map[uint32]*autogen.UpstreamInfo, len(in))
	for k, v := range in {
		res[k] = toUpstreamInfoProto(v)
	}
	return res
}

func toUpstreamInfoProto(in *message.UpstreamInfo) *autogen.UpstreamInfo {
	if in == nil {
		return &autogen.UpstreamInfo{}
	}
	return &autogen.UpstreamInfo{
		SessionId:    in.SessionID,
		SourceNodeId: in.SourceNodeID,
		StreamId:     in.StreamID[:],
	}
}

func toStreamChunkProto(in *message.StreamChunk) (*autogen.StreamChunk, error) {
	if in == nil {
		return &autogen.StreamChunk{}, nil
	}
	dpgs, err := toDataPointGroupsProto(in.DataPointGroups)
	if err != nil {
		return nil, err
	}
	return &autogen.StreamChunk{
		SequenceNumber:  in.SequenceNumber,
		DataPointGroups: dpgs,
	}, nil
}

func toDataPointGroupsProto(in []*message.DataPointGroup) ([]*autogen.DataPointGroup, error) {
	res := make([]*autogen.DataPointGroup, 0, len(in))
	for _, v := range in {
		dpg, err := toDataPointGroupProto(v)
		if err != nil {
			return nil, err
		}
		res = append(res, dpg)
	}
	return res, nil
}

func toDataPointGroupProto(in *message.DataPointGroup) (*autogen.DataPointGroup, error) {
	if in == nil {
		return &autogen.DataPointGroup{}, nil
	}
	res := &autogen.DataPointGroup{
		DataPoints: toDataPointsProto(in.DataPoints),
	}
	if err := toDataIDOrAliasProto(in.DataIDOrAlias, res); err != nil {
		return nil, err
	}

	return res, nil
}

func toDataIDOrAliasProto(in message.DataIDOrAlias, out *autogen.DataPointGroup) error {
	if in == nil {
		return nil
	}
	switch v := in.(type) {
	case *message.DataID:
		out.DataIdOrAlias = &autogen.DataPointGroup_DataId{
			DataId: &autogen.DataID{
				Name: v.Name,
				Type: v.Type,
			},
		}
	case *message.DataIDAlias:
		out.DataIdOrAlias = &autogen.DataPointGroup_DataIdAlias{
			DataIdAlias: uint32(*v),
		}
	case message.DataIDAlias:
		out.DataIdOrAlias = &autogen.DataPointGroup_DataIdAlias{
			DataIdAlias: uint32(v),
		}
	default:
		return errors.Errorf("invalid DataIDOrAlias %v %T: %w", in, in, errors.ErrMalformedMessage)
	}
	return nil
}

func toDownstreamFilterReferencesProto(in [][]*message.DownstreamFilterReference) []*autogen.DownstreamFilterReferences {
	if in == nil {
		return nil
	}
	res := make([]*autogen.DownstreamFilterReferences, 0, len(in))
	for _, group := range in {
		refs := make([]*autogen.DownstreamFilterReference, 0, len(group))
		for _, v := range group {
			if v != nil {
				refs = append(refs, &autogen.DownstreamFilterReference{
					DownstreamFilterIndex: v.DownstreamFilterIndex,
					DataFilterIndex:       v.DataFilterIndex,
				})
			}
		}
		res = append(res, &autogen.DownstreamFilterReferences{
			References: refs,
		})
	}
	return res
}

func errorConvertToProto(m message.Message, err error) error {
	return errors.Errorf("failed to wire message %T: %w", m, err)
}
