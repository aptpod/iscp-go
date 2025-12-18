package convert_test

import (
	"time"

	autogen "github.com/aptpod/iscp-proto/gen/gogofast/iscp2/v1"
	autogenextensions "github.com/aptpod/iscp-proto/gen/gogofast/iscp2/v1/extensions"
	uuid "github.com/google/uuid"

	"github.com/aptpod/iscp-go/message"
)

var (
	pingPB = &autogen.Message{Message: &autogen.Message_Ping{Ping: &autogen.Ping{
		RequestId:       1,
		ExtensionFields: &autogenextensions.PingExtensionFields{},
	}}}
	ping = &message.Ping{
		RequestID:       1,
		ExtensionFields: &message.PingExtensionFields{},
	}
	pongPB = &autogen.Message{Message: &autogen.Message_Pong{Pong: &autogen.Pong{
		RequestId:       1,
		ExtensionFields: &autogenextensions.PongExtensionFields{},
	}}}
	pong = &message.Pong{
		RequestID:       1,
		ExtensionFields: &message.PongExtensionFields{},
	}
	connectRequestPB = &autogen.Message{Message: &autogen.Message_ConnectRequest{
		ConnectRequest: &autogen.ConnectRequest{
			RequestId:       1,
			ProtocolVersion: "ProtocolVersion",
			NodeId:          "NodeId",
			PingTimeout:     2,
			PingInterval:    3,
			ExtensionFields: &autogenextensions.ConnectRequestExtensionFields{
				AccessToken: "accessToken",
				Intdash: &autogenextensions.IntdashExtensionFields{
					ProjectUuid: "b1b25c80-63af-4c8a-b323-28d104990a2f",
				},
			},
		},
	}}
	connectRequest = &message.ConnectRequest{
		RequestID:       1,
		ProtocolVersion: "ProtocolVersion",
		NodeID:          "NodeId",
		PingTimeout:     time.Second * 2,
		PingInterval:    time.Second * 3,
		ExtensionFields: &message.ConnectRequestExtensionFields{
			AccessToken: "accessToken",
			Intdash: &message.IntdashExtensionFields{
				ProjectUUID: uuid.MustParse("b1b25c80-63af-4c8a-b323-28d104990a2f"),
			},
		},
	}
	connectResponsePB = &autogen.Message{Message: &autogen.Message_ConnectResponse{
		ConnectResponse: &autogen.ConnectResponse{
			RequestId:       1,
			ProtocolVersion: "ProtocolVersion",
			ResultCode:      autogen.ResultCode_SUCCEEDED,
			ResultString:    "ResultString",
			ExtensionFields: &autogenextensions.ConnectResponseExtensionFields{},
		},
	}}
	connectResponse = &message.ConnectResponse{
		RequestID:       1,
		ProtocolVersion: "ProtocolVersion",
		ResultCode:      message.ResultCodeSucceeded,
		ResultString:    "ResultString",
		ExtensionFields: &message.ConnectResponseExtensionFields{},
	}
	disconnectPB = &autogen.Message{Message: &autogen.Message_Disconnect{
		Disconnect: &autogen.Disconnect{
			ResultCode:      autogen.ResultCode_SUCCEEDED,
			ResultString:    "ResultString",
			ExtensionFields: &autogenextensions.DisconnectExtensionFields{},
		},
	}}
	disconnect = &message.Disconnect{
		ResultCode:      message.ResultCodeSucceeded,
		ResultString:    "ResultString",
		ExtensionFields: &message.DisconnectExtensionFields{},
	}
	downstreamOpenRequestPB = &autogen.Message{Message: &autogen.Message_DownstreamOpenRequest{
		DownstreamOpenRequest: &autogen.DownstreamOpenRequest{
			RequestId:            1,
			DesiredStreamIdAlias: 2,
			DownstreamFilters: []*autogen.DownstreamFilter{
				{
					SourceNodeId: "SourceNodeId",
					DataFilters: []*autogen.DataFilter{
						{
							Name: "Name",
							Type: "Type",
						},
					},
				},
			},
			ExpiryInterval: 3,
			DataIdAliases: map[uint32]*autogen.DataID{
				1: {
					Name: "Name",
					Type: "Type",
				},
			},
			Qos:               autogen.QoS_RELIABLE,
			ExtensionFields:   &autogenextensions.DownstreamOpenRequestExtensionFields{},
			OmitEmptyChunk:    true,
			EnableResumeToken: true,
		},
	}}
	downstreamOpenRequest = &message.DownstreamOpenRequest{
		RequestID:            1,
		DesiredStreamIDAlias: 2,
		DownstreamFilters: []*message.DownstreamFilter{
			{
				SourceNodeID: "SourceNodeId",
				DataFilters: []*message.DataFilter{
					{
						Name: "Name",
						Type: "Type",
					},
				},
			},
		},
		ExpiryInterval: 3 * time.Second,
		DataIDAliases: map[uint32]*message.DataID{
			1: {
				Name: "Name",
				Type: "Type",
			},
		},
		QoS:               message.QoSReliable,
		ExtensionFields:   &message.DownstreamOpenRequestExtensionFields{},
		OmitEmptyChunk:    true,
		EnableResumeToken: true,
	}
	downstreamOpenResponsePB = &autogen.Message{Message: &autogen.Message_DownstreamOpenResponse{
		DownstreamOpenResponse: &autogen.DownstreamOpenResponse{
			RequestId:        1,
			AssignedStreamId: mustMarshalBinary(uuid.MustParse("11111111-1111-1111-1111-111111111111")),
			ServerTime:       time.Unix(1, 0).UTC().UnixNano(),
			ResultCode:       autogen.ResultCode_SUCCEEDED,
			ResultString:     "ResultString",
			ExtensionFields:  &autogenextensions.DownstreamOpenResponseExtensionFields{},
			ResumeToken:      "downstream-resume-token-1",
		},
	}}
	downstreamOpenResponse = &message.DownstreamOpenResponse{
		RequestID:        1,
		AssignedStreamID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		ResultCode:       message.ResultCodeSucceeded,
		ServerTime:       time.Unix(1, 0).UTC(),
		ResultString:     "ResultString",
		ExtensionFields:  &message.DownstreamOpenResponseExtensionFields{},
		ResumeToken:      "downstream-resume-token-1",
	}
	downstreamOpenResponseServerTimeZeroPB = &autogen.Message{Message: &autogen.Message_DownstreamOpenResponse{
		DownstreamOpenResponse: &autogen.DownstreamOpenResponse{
			RequestId:        1,
			AssignedStreamId: mustMarshalBinary(uuid.MustParse("11111111-1111-1111-1111-111111111111")),
			ResultCode:       autogen.ResultCode_SUCCEEDED,
			ResultString:     "ResultString",
			ExtensionFields:  &autogenextensions.DownstreamOpenResponseExtensionFields{},
		},
	}}

	downstreamOpenResponseServerTimeZero = &message.DownstreamOpenResponse{
		RequestID:        1,
		AssignedStreamID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		ResultCode:       message.ResultCodeSucceeded,
		ResultString:     "ResultString",
		ExtensionFields:  &message.DownstreamOpenResponseExtensionFields{},
	}
	downstreamResumeRequestPB = &autogen.Message{Message: &autogen.Message_DownstreamResumeRequest{
		DownstreamResumeRequest: &autogen.DownstreamResumeRequest{
			RequestId:            1,
			StreamId:             mustMarshalBinary(uuid.MustParse("11111111-1111-1111-1111-111111111111")),
			DesiredStreamIdAlias: 2,
			ExtensionFields:      &autogenextensions.DownstreamResumeRequestExtensionFields{},
			ResumeToken:          "downstream-resume-token-1",
		},
	}}
	downstreamResumeRequest = &message.DownstreamResumeRequest{
		RequestID:            1,
		StreamID:             uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		DesiredStreamIDAlias: 2,
		ExtensionFields:      &message.DownstreamResumeRequestExtensionFields{},
		ResumeToken:          "downstream-resume-token-1",
	}
	downstreamResumeResponsePB = &autogen.Message{Message: &autogen.Message_DownstreamResumeResponse{
		DownstreamResumeResponse: &autogen.DownstreamResumeResponse{
			RequestId:       1,
			ResultCode:      autogen.ResultCode_SUCCEEDED,
			ResultString:    "ResultString",
			ExtensionFields: &autogenextensions.DownstreamResumeResponseExtensionFields{},
			ResumeToken:     "downstream-resume-token-2",
		},
	}}
	downstreamResumeResponse = &message.DownstreamResumeResponse{
		RequestID:       1,
		ResultCode:      message.ResultCodeSucceeded,
		ResultString:    "ResultString",
		ExtensionFields: &message.DownstreamResumeResponseExtensionFields{},
		ResumeToken:     "downstream-resume-token-2",
	}
	downstreamCloseRequestPB = &autogen.Message{Message: &autogen.Message_DownstreamCloseRequest{
		DownstreamCloseRequest: &autogen.DownstreamCloseRequest{
			RequestId:       1,
			StreamId:        mustMarshalBinary(uuid.MustParse("11111111-1111-1111-1111-111111111111")),
			ExtensionFields: &autogenextensions.DownstreamCloseRequestExtensionFields{},
		},
	}}
	downstreamCloseRequest = &message.DownstreamCloseRequest{
		RequestID:       1,
		StreamID:        uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		ExtensionFields: &message.DownstreamCloseRequestExtensionFields{},
	}
	downstreamCloseResponsePB = &autogen.Message{Message: &autogen.Message_DownstreamCloseResponse{
		DownstreamCloseResponse: &autogen.DownstreamCloseResponse{
			RequestId:       1,
			ResultCode:      autogen.ResultCode_SUCCEEDED,
			ResultString:    "ResultString",
			ExtensionFields: &autogenextensions.DownstreamCloseResponseExtensionFields{},
		},
	}}
	downstreamCloseResponse = &message.DownstreamCloseResponse{
		RequestID:       1,
		ResultCode:      message.ResultCodeSucceeded,
		ResultString:    "ResultString",
		ExtensionFields: &message.DownstreamCloseResponseExtensionFields{},
	}
	upstreamCallPB = &autogen.Message{Message: &autogen.Message_UpstreamCall{
		UpstreamCall: &autogen.UpstreamCall{
			CallId:            "CallId",
			RequestCallId:     "RequestCallId",
			DestinationNodeId: "DestinationNodeId",
			Name:              "Name",
			Type:              "Type",
			Payload:           []byte("payload"),
			ExtensionFields:   &autogenextensions.UpstreamCallExtensionFields{},
		},
	}}
	upstreamCall = &message.UpstreamCall{
		CallID:            "CallId",
		RequestCallID:     "RequestCallId",
		DestinationNodeID: "DestinationNodeId",
		Name:              "Name",
		Type:              "Type",
		Payload:           []byte("payload"),
		ExtensionFields:   &message.UpstreamCallExtensionFields{},
	}
	upstreamCallAckPB = &autogen.Message{Message: &autogen.Message_UpstreamCallAck{
		UpstreamCallAck: &autogen.UpstreamCallAck{
			CallId:          "CallId",
			ResultCode:      autogen.ResultCode_SUCCEEDED,
			ResultString:    "ResultString",
			ExtensionFields: &autogenextensions.UpstreamCallAckExtensionFields{},
		},
	}}
	upstreamCallAck = &message.UpstreamCallAck{
		CallID:          "CallId",
		ResultCode:      message.ResultCodeSucceeded,
		ResultString:    "ResultString",
		ExtensionFields: &message.UpstreamCallAckExtensionFields{},
	}
	downstreamCallPB = &autogen.Message{Message: &autogen.Message_DownstreamCall{
		DownstreamCall: &autogen.DownstreamCall{
			CallId:          "CallId",
			RequestCallId:   "RequestCallId",
			SourceNodeId:    "SourceNodeId",
			Name:            "Name",
			Type:            "Type",
			Payload:         []byte("payload"),
			ExtensionFields: &autogenextensions.DownstreamCallExtensionFields{},
		},
	}}
	downstreamCall = &message.DownstreamCall{
		CallID:          "CallId",
		RequestCallID:   "RequestCallId",
		SourceNodeID:    "SourceNodeId",
		Name:            "Name",
		Type:            "Type",
		Payload:         []byte("payload"),
		ExtensionFields: &message.DownstreamCallExtensionFields{},
	}
	upstreamMetadataPB = &autogen.Message{Message: &autogen.Message_UpstreamMetadata{
		UpstreamMetadata: &autogen.UpstreamMetadata{
			RequestId: 1,
			Metadata: &autogen.UpstreamMetadata_BaseTime{
				BaseTime: &autogen.BaseTime{
					SessionId:   "SessionId",
					Name:        "Name",
					Priority:    1,
					ElapsedTime: 2,
					BaseTime:    3,
				},
			},
			ExtensionFields: &autogenextensions.UpstreamMetadataExtensionFields{
				Persist: true,
			},
		},
	}}
	upstreamMetadata = &message.UpstreamMetadata{
		RequestID: 1,
		Metadata: &message.BaseTime{
			SessionID:   "SessionId",
			Name:        "Name",
			Priority:    1,
			ElapsedTime: 2,
			BaseTime:    time.Unix(0, 3).UTC(),
		},
		ExtensionFields: &message.UpstreamMetadataExtensionFields{
			Persist: true,
		},
	}
	upstreamMetadataAckPB = &autogen.Message{Message: &autogen.Message_UpstreamMetadataAck{
		UpstreamMetadataAck: &autogen.UpstreamMetadataAck{
			RequestId:       1,
			ResultCode:      autogen.ResultCode_SUCCEEDED,
			ResultString:    "ResultString",
			ExtensionFields: &autogenextensions.UpstreamMetadataAckExtensionFields{},
		},
	}}
	upstreamMetadataAck = &message.UpstreamMetadataAck{
		RequestID:       1,
		ResultCode:      message.ResultCodeSucceeded,
		ResultString:    "ResultString",
		ExtensionFields: &message.UpstreamMetadataAckExtensionFields{},
	}
	downstreamMetadataPB = &autogen.Message{Message: &autogen.Message_DownstreamMetadata{
		DownstreamMetadata: &autogen.DownstreamMetadata{
			RequestId:     1,
			StreamIdAlias: 2,
			SourceNodeId:  "SourceNodeId",
			Metadata: &autogen.DownstreamMetadata_BaseTime{BaseTime: &autogen.BaseTime{
				SessionId:   "SessionId",
				Name:        "Name",
				Priority:    1,
				ElapsedTime: 2,
				BaseTime:    3,
			}},
			ExtensionFields: &autogenextensions.DownstreamMetadataExtensionFields{},
		},
	}}
	downstreamMetadata = &message.DownstreamMetadata{
		RequestID:     1,
		StreamIDAlias: 2,
		SourceNodeID:  "SourceNodeId",
		Metadata: &message.BaseTime{
			SessionID:   "SessionId",
			Name:        "Name",
			Priority:    1,
			ElapsedTime: 2,
			BaseTime:    time.Unix(0, 3).UTC(),
		},
		ExtensionFields: &message.DownstreamMetadataExtensionFields{},
	}
	downstreamMetadataAckPB = &autogen.Message{Message: &autogen.Message_DownstreamMetadataAck{
		DownstreamMetadataAck: &autogen.DownstreamMetadataAck{
			RequestId:       1,
			ResultCode:      autogen.ResultCode_SUCCEEDED,
			ResultString:    "ResultString",
			ExtensionFields: &autogenextensions.DownstreamMetadataAckExtensionFields{},
		},
	}}
	downstreamMetadataAck = &message.DownstreamMetadataAck{
		RequestID:       1,
		ResultCode:      message.ResultCodeSucceeded,
		ResultString:    "ResultString",
		ExtensionFields: &message.DownstreamMetadataAckExtensionFields{},
	}
	upstreamOpenRequestPB = &autogen.Message{Message: &autogen.Message_UpstreamOpenRequest{
		UpstreamOpenRequest: &autogen.UpstreamOpenRequest{
			RequestId:      1,
			SessionId:      "SessionId",
			AckInterval:    2,
			ExpiryInterval: 4,
			DataIds: []*autogen.DataID{
				{
					Name: "Name",
					Type: "Type",
				},
			},
			Qos: autogen.QoS_RELIABLE,
			ExtensionFields: &autogenextensions.UpstreamOpenRequestExtensionFields{
				Persist: true,
			},
			EnableResumeToken: true,
		},
	}}
	upstreamOpenRequest = &message.UpstreamOpenRequest{
		RequestID:      1,
		SessionID:      "SessionId",
		AckInterval:    2 * time.Millisecond,
		ExpiryInterval: 4 * time.Second,
		DataIDs: []*message.DataID{
			{
				Name: "Name",
				Type: "Type",
			},
		},
		QoS: message.QoSReliable,
		ExtensionFields: &message.UpstreamOpenRequestExtensionFields{
			Persist: true,
		},
		EnableResumeToken: true,
	}

	upstreamOpenResponsePB = &autogen.Message{Message: &autogen.Message_UpstreamOpenResponse{
		UpstreamOpenResponse: &autogen.UpstreamOpenResponse{
			RequestId:             1,
			AssignedStreamId:      mustMarshalBinary(uuid.MustParse("11111111-1111-1111-1111-111111111111")),
			AssignedStreamIdAlias: 2,
			ResultCode:            autogen.ResultCode_SUCCEEDED,
			ServerTime:            3,
			ResultString:          "ResultString",
			DataIdAliases: map[uint32]*autogen.DataID{
				1: {
					Name: "Name",
					Type: "Type",
				},
			},
			ExtensionFields: &autogenextensions.UpstreamOpenResponseExtensionFields{},
			ResumeToken:     "upstream-resume-token-1",
		},
	}}
	upstreamOpenResponse = &message.UpstreamOpenResponse{
		RequestID:             1,
		AssignedStreamID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		AssignedStreamIDAlias: 2,
		ResultCode:            message.ResultCodeSucceeded,
		ResultString:          "ResultString",
		ServerTime:            time.Unix(0, 3).UTC(),
		DataIDAliases: map[uint32]*message.DataID{
			1: {
				Name: "Name",
				Type: "Type",
			},
		},
		ExtensionFields: &message.UpstreamOpenResponseExtensionFields{},
		ResumeToken:     "upstream-resume-token-1",
	}
	upstreamOpenResponseServerTimeZeroPB = &autogen.Message{Message: &autogen.Message_UpstreamOpenResponse{
		UpstreamOpenResponse: &autogen.UpstreamOpenResponse{
			RequestId:             1,
			AssignedStreamId:      mustMarshalBinary(uuid.MustParse("11111111-1111-1111-1111-111111111111")),
			AssignedStreamIdAlias: 2,
			ResultCode:            autogen.ResultCode_SUCCEEDED,
			ResultString:          "ResultString",
			DataIdAliases: map[uint32]*autogen.DataID{
				1: {
					Name: "Name",
					Type: "Type",
				},
			},
			ExtensionFields: &autogenextensions.UpstreamOpenResponseExtensionFields{},
		},
	}}
	upstreamOpenResponseServerTimeZero = &message.UpstreamOpenResponse{
		RequestID:             1,
		AssignedStreamID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		AssignedStreamIDAlias: 2,
		ResultCode:            message.ResultCodeSucceeded,
		ResultString:          "ResultString",
		DataIDAliases: map[uint32]*message.DataID{
			1: {
				Name: "Name",
				Type: "Type",
			},
		},
		ExtensionFields: &message.UpstreamOpenResponseExtensionFields{},
	}
	upstreamResumeRequestPB = &autogen.Message{Message: &autogen.Message_UpstreamResumeRequest{
		UpstreamResumeRequest: &autogen.UpstreamResumeRequest{
			RequestId:       1,
			StreamId:        mustMarshalBinary(uuid.MustParse("11111111-1111-1111-1111-111111111111")),
			ExtensionFields: &autogenextensions.UpstreamResumeRequestExtensionFields{},
			ResumeToken:     "upstream-resume-token-1",
		},
	}}
	upstreamResumeRequest = &message.UpstreamResumeRequest{
		RequestID:       1,
		StreamID:        uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		ExtensionFields: &message.UpstreamResumeRequestExtensionFields{},
		ResumeToken:     "upstream-resume-token-1",
	}
	upstreamResumeResponsePB = &autogen.Message{Message: &autogen.Message_UpstreamResumeResponse{
		UpstreamResumeResponse: &autogen.UpstreamResumeResponse{
			RequestId:             1,
			AssignedStreamIdAlias: 2,
			ResultCode:            autogen.ResultCode_SUCCEEDED,
			ResultString:          "ResultString",
			ExtensionFields:       &autogenextensions.UpstreamResumeResponseExtensionFields{},
			ResumeToken:           "upstream-resume-token-2",
		},
	}}
	upstreamResumeResponse = &message.UpstreamResumeResponse{
		RequestID:             1,
		AssignedStreamIDAlias: 2,
		ResultCode:            message.ResultCodeSucceeded,
		ResultString:          "ResultString",
		ExtensionFields:       &message.UpstreamResumeResponseExtensionFields{},
		ResumeToken:           "upstream-resume-token-2",
	}
	upstreamCloseRequestPB = &autogen.Message{Message: &autogen.Message_UpstreamCloseRequest{
		UpstreamCloseRequest: &autogen.UpstreamCloseRequest{
			RequestId:           1,
			StreamId:            mustMarshalBinary(uuid.MustParse("11111111-1111-1111-1111-111111111111")),
			TotalDataPoints:     2,
			FinalSequenceNumber: 3,
			ExtensionFields: &autogenextensions.UpstreamCloseRequestExtensionFields{
				CloseSession: true,
			},
		},
	}}
	upstreamCloseRequest = &message.UpstreamCloseRequest{
		RequestID:           1,
		StreamID:            uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		TotalDataPoints:     2,
		FinalSequenceNumber: 3,
		ExtensionFields: &message.UpstreamCloseRequestExtensionFields{
			CloseSession: true,
		},
	}
	upstreamCloseResponsePB = &autogen.Message{Message: &autogen.Message_UpstreamCloseResponse{
		UpstreamCloseResponse: &autogen.UpstreamCloseResponse{
			RequestId:       1,
			ResultCode:      autogen.ResultCode_SUCCEEDED,
			ResultString:    "ResultString",
			ExtensionFields: &autogenextensions.UpstreamCloseResponseExtensionFields{},
		},
	}}
	upstreamCloseResponse = &message.UpstreamCloseResponse{
		RequestID:       1,
		ResultCode:      message.ResultCodeSucceeded,
		ResultString:    "ResultString",
		ExtensionFields: &message.UpstreamCloseResponseExtensionFields{},
	}

	upstreamDataPointsPB = &autogen.Message{Message: &autogen.Message_UpstreamChunk{UpstreamChunk: &autogen.UpstreamChunk{
		StreamIdAlias: 1,
		StreamChunk: &autogen.StreamChunk{
			SequenceNumber: 2,
			DataPointGroups: []*autogen.DataPointGroup{
				{
					DataIdOrAlias: &autogen.DataPointGroup_DataIdAlias{DataIdAlias: 3},
					DataPoints:    []*autogen.DataPoint{{ElapsedTime: 4, Payload: []byte{0, 1, 2, 3, 4}}},
				},
			},
		},
		DataIds: []*autogen.DataID{
			{
				Name: "Name",
				Type: "Type",
			},
		},
		ExtensionFields: &autogenextensions.UpstreamChunkExtensionFields{},
	}}}
	upstreamDataPoints = &message.UpstreamChunk{
		StreamIDAlias: 1,
		StreamChunk: &message.StreamChunk{
			SequenceNumber: 2,
			DataPointGroups: []*message.DataPointGroup{
				{
					DataIDOrAlias: message.DataIDAlias(3),
					DataPoints:    []*message.DataPoint{{ElapsedTime: 4, Payload: []byte{0, 1, 2, 3, 4}}},
				},
			},
		},
		DataIDs: []*message.DataID{
			{
				Name: "Name",
				Type: "Type",
			},
		},
		ExtensionFields: &message.UpstreamChunkExtensionFields{},
	}
	upstreamDataPointsAckPB = &autogen.Message{Message: &autogen.Message_UpstreamChunkAck{
		UpstreamChunkAck: &autogen.UpstreamChunkAck{
			StreamIdAlias: 1,
			Results: []*autogen.UpstreamChunkResult{{
				SequenceNumber:  1,
				ResultCode:      autogen.ResultCode_SUCCEEDED,
				ResultString:    "SUCCEEDED",
				ExtensionFields: &autogenextensions.UpstreamChunkResultExtensionFields{},
			}},
			DataIdAliases: map[uint32]*autogen.DataID{
				1: {
					Name: "name",
					Type: "type",
				},
			},
			ExtensionFields: &autogenextensions.UpstreamChunkAckExtensionFields{},
		},
	}}
	upstreamDataPointsAck = &message.UpstreamChunkAck{
		StreamIDAlias: 1,
		Results: []*message.UpstreamChunkResult{
			{
				SequenceNumber:  1,
				ResultCode:      message.ResultCodeSucceeded,
				ResultString:    "SUCCEEDED",
				ExtensionFields: &message.UpstreamChunkResultExtensionFields{},
			},
		},
		DataIDAliases: map[uint32]*message.DataID{
			1: {
				Name: "name",
				Type: "type",
			},
		},
		ExtensionFields: &message.UpstreamChunkAckExtensionFields{},
	}
	downstreamDataPointsPB = &autogen.Message{Message: &autogen.Message_DownstreamChunk{
		DownstreamChunk: &autogen.DownstreamChunk{
			StreamIdAlias: 1,
			UpstreamOrAlias: &autogen.DownstreamChunk_UpstreamAlias{
				UpstreamAlias: 2,
			},
			StreamChunk: &autogen.StreamChunk{
				SequenceNumber: 3,
				DataPointGroups: []*autogen.DataPointGroup{
					{
						DataIdOrAlias: &autogen.DataPointGroup_DataIdAlias{DataIdAlias: 4},
						DataPoints:    []*autogen.DataPoint{{ElapsedTime: 5, Payload: []byte{0, 1, 2, 3, 4}}},
					},
				},
			},
			DownstreamFilterReferences: []*autogen.DownstreamFilterReferences{
				{
					References: []*autogen.DownstreamFilterReference{
						{DataFilterIndex: 4},
						{DataFilterIndex: 5},
						{DataFilterIndex: 6},
					},
				},
			},
			ExtensionFields: &autogenextensions.DownstreamChunkExtensionFields{},
		},
	}}
	downstreamDataPoints = &message.DownstreamChunk{
		StreamIDAlias:   1,
		UpstreamOrAlias: message.UpstreamAlias(2),
		StreamChunk: &message.StreamChunk{
			SequenceNumber: 3,
			DataPointGroups: []*message.DataPointGroup{
				{
					DataIDOrAlias: message.DataIDAlias(4),
					DataPoints: []*message.DataPoint{
						{
							ElapsedTime: 5,
							Payload:     []byte{0, 1, 2, 3, 4},
						},
					},
				},
			},
		},
		DownstreamFilterReferences: [][]*message.DownstreamFilterReference{
			{
				{DataFilterIndex: 4},
				{DataFilterIndex: 5},
				{DataFilterIndex: 6},
			},
		},
		ExtensionFields: &message.DownstreamChunkExtensionFields{},
	}
	downstreamDataPointsAckPB = &autogen.Message{Message: &autogen.Message_DownstreamChunkAck{
		DownstreamChunkAck: &autogen.DownstreamChunkAck{
			StreamIdAlias: 1,
			AckId:         2,
			Results: []*autogen.DownstreamChunkResult{{
				SequenceNumberInUpstream: 2,
				StreamIdOfUpstream:       mustMarshalBinary(uuid.MustParse("11111111-1111-1111-1111-111111111111")),
				ResultCode:               autogen.ResultCode_SUCCEEDED,
				ResultString:             "SUCCEEDED",
				ExtensionFields:          &autogenextensions.DownstreamChunkResultExtensionFields{},
			}},
			UpstreamAliases: map[uint32]*autogen.UpstreamInfo{
				1: {
					SessionId:    "SessionId",
					StreamId:     mustMarshalBinary(uuid.MustParse("22222222-2222-2222-2222-222222222222")),
					SourceNodeId: "SourceNodeId",
				},
			},
			DataIdAliases: map[uint32]*autogen.DataID{
				1: {
					Name: "Name",
					Type: "Type",
				},
			},
			ExtensionFields: &autogenextensions.DownstreamChunkAckExtensionFields{},
		},
	}}

	downstreamDataPointsAck = &message.DownstreamChunkAck{
		StreamIDAlias: 1,
		AckID:         2,
		Results: []*message.DownstreamChunkResult{{
			SequenceNumberInUpstream: 2,
			StreamIDOfUpstream:       uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			ResultCode:               message.ResultCodeSucceeded,
			ResultString:             "SUCCEEDED",
			ExtensionFields:          &message.DownstreamChunkResultExtensionFields{},
		}},
		UpstreamAliases: map[uint32]*message.UpstreamInfo{
			1: {
				SessionID:    "SessionId",
				StreamID:     uuid.MustParse("22222222-2222-2222-2222-222222222222"),
				SourceNodeID: "SourceNodeId",
			},
		},
		DataIDAliases: map[uint32]*message.DataID{
			1: {
				Name: "Name",
				Type: "Type",
			},
		},
		ExtensionFields: &message.DownstreamChunkAckExtensionFields{},
	}

	downstreamDataPointsAckCompletePB = &autogen.Message{
		Message: &autogen.Message_DownstreamChunkAckComplete{
			DownstreamChunkAckComplete: &autogen.DownstreamChunkAckComplete{
				StreamIdAlias:   1,
				AckId:           2,
				ResultCode:      autogen.ResultCode_SUCCEEDED,
				ResultString:    "SUCCEEDED",
				ExtensionFields: &autogenextensions.DownstreamChunkAckCompleteExtensionFields{},
			},
		},
	}
	downstreamDataPointsAckComplete = &message.DownstreamChunkAckComplete{
		StreamIDAlias:   1,
		AckID:           2,
		ResultCode:      message.ResultCodeSucceeded,
		ResultString:    "SUCCEEDED",
		ExtensionFields: &message.DownstreamChunkAckCompleteExtensionFields{},
	}
)

// metadata proto
var (
	downstreamMetadataBaseTimePB = &autogen.DownstreamMetadata{
		RequestId:    1,
		SourceNodeId: "SourceNodeId",
		Metadata: &autogen.DownstreamMetadata_BaseTime{BaseTime: &autogen.BaseTime{
			SessionId:   "SessionId",
			Name:        "Name",
			Priority:    1,
			ElapsedTime: 2,
			BaseTime:    3,
		}},
		ExtensionFields: &autogenextensions.DownstreamMetadataExtensionFields{},
	}
	upstreamMetadataBaseTimePB = &autogen.UpstreamMetadata{
		RequestId: 1,
		Metadata: &autogen.UpstreamMetadata_BaseTime{
			BaseTime: &autogen.BaseTime{
				SessionId:   "SessionId",
				Name:        "Name",
				Priority:    1,
				ElapsedTime: 2,
				BaseTime:    3,
			},
		},
		ExtensionFields: &autogenextensions.UpstreamMetadataExtensionFields{
			Persist: true,
		},
	}
	metadataBaseTime = &message.BaseTime{
		SessionID:   "SessionId",
		Name:        "Name",
		Priority:    1,
		ElapsedTime: 2,
		BaseTime:    time.Unix(0, 3).UTC(),
	}
	downstreamMetadataUpstreamOpenPB = &autogen.DownstreamMetadata{
		RequestId:    1,
		SourceNodeId: "SourceNodeId",
		Metadata: &autogen.DownstreamMetadata_UpstreamOpen{
			UpstreamOpen: &autogen.UpstreamOpen{
				StreamId:  mustMarshalBinary(uuid.MustParse("11111111-1111-1111-1111-111111111111")),
				SessionId: "SessionId",
				Qos:       autogen.QoS_RELIABLE,
			},
		},
		ExtensionFields: &autogenextensions.DownstreamMetadataExtensionFields{},
	}
	metadataUpstreamOpen = &message.UpstreamOpen{
		StreamID:  uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		SessionID: "SessionId",
		QoS:       message.QoSReliable,
	}
	downstreamMetadataUpstreamAbnormalClosePB = &autogen.DownstreamMetadata{
		RequestId:    1,
		SourceNodeId: "SourceNodeId",
		Metadata: &autogen.DownstreamMetadata_UpstreamAbnormalClose{
			UpstreamAbnormalClose: &autogen.UpstreamAbnormalClose{
				StreamId:  mustMarshalBinary(uuid.MustParse("11111111-1111-1111-1111-111111111111")),
				SessionId: "SessionId",
			},
		},
		ExtensionFields: &autogenextensions.DownstreamMetadataExtensionFields{},
	}
	metadataUpstreamAbnormalClose = &message.UpstreamAbnormalClose{
		StreamID:  uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		SessionID: "SessionId",
	}
	downstreamMetadataUpstreamResumePB = &autogen.DownstreamMetadata{
		RequestId:    1,
		SourceNodeId: "SourceNodeId",
		Metadata: &autogen.DownstreamMetadata_UpstreamResume{
			UpstreamResume: &autogen.UpstreamResume{
				StreamId:  mustMarshalBinary(uuid.MustParse("11111111-1111-1111-1111-111111111111")),
				SessionId: "SessionId",
				Qos:       autogen.QoS_PARTIAL,
			},
		},
		ExtensionFields: &autogenextensions.DownstreamMetadataExtensionFields{},
	}
	metadataUpstreamResume = &message.UpstreamResume{
		StreamID:  uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		QoS:       message.QoSPartial,
		SessionID: "SessionId",
	}
	downstreamMetadataUpstreamNormalClosePB = &autogen.DownstreamMetadata{
		RequestId:    1,
		SourceNodeId: "SourceNodeId",
		Metadata: &autogen.DownstreamMetadata_UpstreamNormalClose{UpstreamNormalClose: &autogen.UpstreamNormalClose{
			StreamId:            mustMarshalBinary(uuid.MustParse("11111111-1111-1111-1111-111111111111")),
			SessionId:           "11111111-1111-1111-1111-111111111111",
			TotalDataPoints:     1,
			FinalSequenceNumber: 2,
		}},
		ExtensionFields: &autogenextensions.DownstreamMetadataExtensionFields{},
	}
	metadataUpstreamNormalClose = &message.UpstreamNormalClose{
		StreamID:            uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		SessionID:           "11111111-1111-1111-1111-111111111111",
		TotalDataPoints:     1,
		FinalSequenceNumber: 2,
	}
	downstreamMetadataDownstreamOpenPB = &autogen.DownstreamMetadata{
		RequestId:    1,
		SourceNodeId: "SourceNodeId",
		Metadata: &autogen.DownstreamMetadata_DownstreamOpen{
			DownstreamOpen: &autogen.DownstreamOpen{
				StreamId: mustMarshalBinary(uuid.MustParse("11111111-1111-1111-1111-111111111111")),
				DownstreamFilters: []*autogen.DownstreamFilter{{
					SourceNodeId: "SourceNodeId",
					DataFilters:  []*autogen.DataFilter{{Name: "Name", Type: "Type"}},
				}},
				Qos: autogen.QoS_RELIABLE,
			},
		},
		ExtensionFields: &autogenextensions.DownstreamMetadataExtensionFields{},
	}
	metadataDownstreamOpen = &message.DownstreamOpen{
		StreamID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		DownstreamFilters: []*message.DownstreamFilter{
			{
				SourceNodeID: "SourceNodeId",
				DataFilters: []*message.DataFilter{
					{
						Name: "Name",
						Type: "Type",
					},
				},
			},
		},
		QoS: message.QoSReliable,
	}
	downstreamMetadataDownstreamAbnormalClosePB = &autogen.DownstreamMetadata{
		RequestId:    1,
		SourceNodeId: "SourceNodeId",
		Metadata: &autogen.DownstreamMetadata_DownstreamAbnormalClose{DownstreamAbnormalClose: &autogen.DownstreamAbnormalClose{
			StreamId: mustMarshalBinary(uuid.MustParse("11111111-1111-1111-1111-111111111111")),
		}},
		ExtensionFields: &autogenextensions.DownstreamMetadataExtensionFields{},
	}
	metadataDownstreamAbnormalClose = &message.DownstreamAbnormalClose{
		StreamID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
	}
	downstreamMetadataDownstreamResumePB = &autogen.DownstreamMetadata{
		RequestId:    1,
		SourceNodeId: "SourceNodeId",
		Metadata: &autogen.DownstreamMetadata_DownstreamResume{
			DownstreamResume: &autogen.DownstreamResume{
				StreamId: mustMarshalBinary(uuid.MustParse("11111111-1111-1111-1111-111111111111")),
				DownstreamFilters: []*autogen.DownstreamFilter{{
					SourceNodeId: "SourceNodeId",
					DataFilters:  []*autogen.DataFilter{{Name: "Name", Type: "Type"}},
				}},
				Qos: autogen.QoS_RELIABLE,
			},
		},
		ExtensionFields: &autogenextensions.DownstreamMetadataExtensionFields{},
	}
	metadataDownstreamResume = &message.DownstreamResume{
		StreamID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		DownstreamFilters: []*message.DownstreamFilter{
			{
				SourceNodeID: "SourceNodeId",
				DataFilters: []*message.DataFilter{
					{
						Name: "Name",
						Type: "Type",
					},
				},
			},
		},
		QoS: message.QoSReliable,
	}
	downstreamMetadataDownstreamNormalClosePB = &autogen.DownstreamMetadata{
		RequestId:    1,
		SourceNodeId: "SourceNodeId",
		Metadata: &autogen.DownstreamMetadata_DownstreamNormalClose{DownstreamNormalClose: &autogen.DownstreamNormalClose{
			StreamId: mustMarshalBinary(uuid.MustParse("11111111-1111-1111-1111-111111111111")),
		}},
		ExtensionFields: &autogenextensions.DownstreamMetadataExtensionFields{},
	}
	metadataDownstreamNormalClose = &message.DownstreamNormalClose{
		StreamID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
	}
)
