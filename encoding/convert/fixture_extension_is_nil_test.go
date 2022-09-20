package convert_test

import (
	"time"

	autogen "github.com/aptpod/iscp-go/encoding/autogen"
	"github.com/aptpod/iscp-go/message"
	uuid "github.com/google/uuid"
)

var (
	pingPBNilExtension = &autogen.Message{Message: &autogen.Message_Ping{Ping: &autogen.Ping{
		RequestId:       1,
		ExtensionFields: nil,
	}}}
	pingNilExtension = &message.Ping{
		RequestID:       1,
		ExtensionFields: nil,
	}
	pongPBNilExtension = &autogen.Message{Message: &autogen.Message_Pong{Pong: &autogen.Pong{
		RequestId:       1,
		ExtensionFields: nil,
	}}}
	pongNilExtension = &message.Pong{
		RequestID:       1,
		ExtensionFields: nil,
	}
	connectRequestPBNilExtension = &autogen.Message{Message: &autogen.Message_ConnectRequest{
		ConnectRequest: &autogen.ConnectRequest{
			RequestId:       1,
			ProtocolVersion: "ProtocolVersion",
			NodeId:          "NodeId",
			PingTimeout:     2,
			PingInterval:    3,
			ExtensionFields: nil,
		},
	}}
	connectRequestNilExtension = &message.ConnectRequest{
		RequestID:       1,
		ProtocolVersion: "ProtocolVersion",
		NodeID:          "NodeId",
		PingTimeout:     time.Second * 2,
		PingInterval:    time.Second * 3,
		ExtensionFields: nil,
	}
	connectResponsePBNilExtension = &autogen.Message{Message: &autogen.Message_ConnectResponse{
		ConnectResponse: &autogen.ConnectResponse{
			RequestId:       1,
			ProtocolVersion: "ProtocolVersion",
			ResultCode:      autogen.ResultCode_SUCCEEDED,
			ResultString:    "ResultString",
			ExtensionFields: nil,
		},
	}}
	connectResponseNilExtension = &message.ConnectResponse{
		RequestID:       1,
		ProtocolVersion: "ProtocolVersion",
		ResultCode:      message.ResultCodeSucceeded,
		ResultString:    "ResultString",
		ExtensionFields: nil,
	}
	disconnectPBNilExtension = &autogen.Message{Message: &autogen.Message_Disconnect{
		Disconnect: &autogen.Disconnect{
			ResultCode:      autogen.ResultCode_SUCCEEDED,
			ResultString:    "ResultString",
			ExtensionFields: nil,
		},
	}}
	disconnectNilExtension = &message.Disconnect{
		ResultCode:      message.ResultCodeSucceeded,
		ResultString:    "ResultString",
		ExtensionFields: nil,
	}
	downstreamOpenRequestPBNilExtension = &autogen.Message{Message: &autogen.Message_DownstreamOpenRequest{
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
			Qos:             autogen.QoS_RELIABLE,
			ExtensionFields: nil,
		},
	}}
	downstreamOpenRequestNilExtension = &message.DownstreamOpenRequest{
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
		QoS:             message.QoSReliable,
		ExtensionFields: nil,
	}
	downstreamOpenResponsePBNilExtension = &autogen.Message{Message: &autogen.Message_DownstreamOpenResponse{
		DownstreamOpenResponse: &autogen.DownstreamOpenResponse{
			RequestId:        1,
			AssignedStreamId: mustMarshalBinary(uuid.MustParse("11111111-1111-1111-1111-111111111111")),
			ServerTime:       time.Unix(1, 0).UnixNano(),
			ResultCode:       autogen.ResultCode_SUCCEEDED,
			ResultString:     "ResultString",
			ExtensionFields:  nil,
		},
	}}
	downstreamOpenResponseNilExtension = &message.DownstreamOpenResponse{
		RequestID:        1,
		AssignedStreamID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		ServerTime:       time.Unix(1, 0).UTC(),
		ResultCode:       message.ResultCodeSucceeded,
		ResultString:     "ResultString",
		ExtensionFields:  nil,
	}
	downstreamResumeRequestPBNilExtension = &autogen.Message{Message: &autogen.Message_DownstreamResumeRequest{
		DownstreamResumeRequest: &autogen.DownstreamResumeRequest{
			RequestId:            1,
			StreamId:             mustMarshalBinary(uuid.MustParse("11111111-1111-1111-1111-111111111111")),
			DesiredStreamIdAlias: 2,
			ExtensionFields:      nil,
		},
	}}
	downstreamResumeRequestNilExtension = &message.DownstreamResumeRequest{
		RequestID:            1,
		StreamID:             uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		DesiredStreamIDAlias: 2,
		ExtensionFields:      nil,
	}
	downstreamResumeResponsePBNilExtension = &autogen.Message{Message: &autogen.Message_DownstreamResumeResponse{
		DownstreamResumeResponse: &autogen.DownstreamResumeResponse{
			RequestId:       1,
			ResultCode:      autogen.ResultCode_SUCCEEDED,
			ResultString:    "ResultString",
			ExtensionFields: nil,
		},
	}}
	downstreamResumeResponseNilExtension = &message.DownstreamResumeResponse{
		RequestID:       1,
		ResultCode:      message.ResultCodeSucceeded,
		ResultString:    "ResultString",
		ExtensionFields: nil,
	}
	downstreamCloseRequestPBNilExtension = &autogen.Message{Message: &autogen.Message_DownstreamCloseRequest{
		DownstreamCloseRequest: &autogen.DownstreamCloseRequest{
			RequestId:       1,
			StreamId:        mustMarshalBinary(uuid.MustParse("11111111-1111-1111-1111-111111111111")),
			ExtensionFields: nil,
		},
	}}
	downstreamCloseRequestNilExtension = &message.DownstreamCloseRequest{
		RequestID:       1,
		StreamID:        uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		ExtensionFields: nil,
	}
	downstreamCloseResponsePBNilExtension = &autogen.Message{Message: &autogen.Message_DownstreamCloseResponse{
		DownstreamCloseResponse: &autogen.DownstreamCloseResponse{
			RequestId:       1,
			ResultCode:      autogen.ResultCode_SUCCEEDED,
			ResultString:    "ResultString",
			ExtensionFields: nil,
		},
	}}
	downstreamCloseResponseNilExtension = &message.DownstreamCloseResponse{
		RequestID:       1,
		ResultCode:      message.ResultCodeSucceeded,
		ResultString:    "ResultString",
		ExtensionFields: nil,
	}
	upstreamCallPBNilExtension = &autogen.Message{Message: &autogen.Message_UpstreamCall{
		UpstreamCall: &autogen.UpstreamCall{
			CallId:            "CallId",
			RequestCallId:     "RequestCallId",
			DestinationNodeId: "DestinationNodeId",
			Name:              "Name",
			Type:              "Type",
			Payload:           []byte("payload"),
			ExtensionFields:   nil,
		},
	}}
	upstreamCallNilExtension = &message.UpstreamCall{
		CallID:            "CallId",
		RequestCallID:     "RequestCallId",
		DestinationNodeID: "DestinationNodeId",
		Name:              "Name",
		Type:              "Type",
		Payload:           []byte("payload"),
		ExtensionFields:   nil,
	}
	upstreamCallAckPBNilExtension = &autogen.Message{Message: &autogen.Message_UpstreamCallAck{
		UpstreamCallAck: &autogen.UpstreamCallAck{
			CallId:          "CallId",
			ResultCode:      autogen.ResultCode_SUCCEEDED,
			ResultString:    "ResultString",
			ExtensionFields: nil,
		},
	}}
	upstreamCallAckNilExtension = &message.UpstreamCallAck{
		CallID:          "CallId",
		ResultCode:      message.ResultCodeSucceeded,
		ResultString:    "ResultString",
		ExtensionFields: nil,
	}
	downstreamCallPBNilExtension = &autogen.Message{Message: &autogen.Message_DownstreamCall{
		DownstreamCall: &autogen.DownstreamCall{
			CallId:          "CallId",
			RequestCallId:   "RequestCallId",
			SourceNodeId:    "SourceNodeId",
			Name:            "Name",
			Type:            "Type",
			Payload:         []byte("payload"),
			ExtensionFields: nil,
		},
	}}
	downstreamCallNilExtension = &message.DownstreamCall{
		CallID:          "CallId",
		RequestCallID:   "RequestCallId",
		SourceNodeID:    "SourceNodeId",
		Name:            "Name",
		Type:            "Type",
		Payload:         []byte("payload"),
		ExtensionFields: nil,
	}
	upstreamMetadataPBNilExtension = &autogen.Message{Message: &autogen.Message_UpstreamMetadata{
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
			ExtensionFields: nil,
		},
	}}
	upstreamMetadataNilExtension = &message.UpstreamMetadata{
		RequestID: 1,
		Metadata: &message.BaseTime{
			SessionID:   "SessionId",
			Name:        "Name",
			Priority:    1,
			ElapsedTime: 2,
			BaseTime:    time.Unix(0, 3).UTC(),
		},
		ExtensionFields: nil,
	}
	upstreamMetadataAckPBNilExtension = &autogen.Message{Message: &autogen.Message_UpstreamMetadataAck{
		UpstreamMetadataAck: &autogen.UpstreamMetadataAck{
			RequestId:       1,
			ResultCode:      autogen.ResultCode_SUCCEEDED,
			ResultString:    "ResultString",
			ExtensionFields: nil,
		},
	}}
	upstreamMetadataAckNilExtension = &message.UpstreamMetadataAck{
		RequestID:       1,
		ResultCode:      message.ResultCodeSucceeded,
		ResultString:    "ResultString",
		ExtensionFields: nil,
	}
	downstreamMetadataPBNilExtension = &autogen.Message{Message: &autogen.Message_DownstreamMetadata{
		DownstreamMetadata: &autogen.DownstreamMetadata{
			RequestId:    1,
			SourceNodeId: "SourceNodeId",
			Metadata: &autogen.DownstreamMetadata_BaseTime{BaseTime: &autogen.BaseTime{
				SessionId:   "SessionId",
				Name:        "Name",
				Priority:    1,
				ElapsedTime: 2,
				BaseTime:    3,
			}},
			ExtensionFields: nil,
		},
	}}
	downstreamMetadataNilExtension = &message.DownstreamMetadata{
		RequestID:    1,
		SourceNodeID: "SourceNodeId",
		Metadata: &message.BaseTime{
			SessionID:   "SessionId",
			Name:        "Name",
			Priority:    1,
			ElapsedTime: 2,
			BaseTime:    time.Unix(0, 3).UTC(),
		},
		ExtensionFields: nil,
	}
	downstreamMetadataAckPBNilExtension = &autogen.Message{Message: &autogen.Message_DownstreamMetadataAck{
		DownstreamMetadataAck: &autogen.DownstreamMetadataAck{
			RequestId:       1,
			ResultCode:      autogen.ResultCode_SUCCEEDED,
			ResultString:    "ResultString",
			ExtensionFields: nil,
		},
	}}
	downstreamMetadataAckNilExtension = &message.DownstreamMetadataAck{
		RequestID:       1,
		ResultCode:      message.ResultCodeSucceeded,
		ResultString:    "ResultString",
		ExtensionFields: nil,
	}
	upstreamOpenRequestPBNilExtension = &autogen.Message{Message: &autogen.Message_UpstreamOpenRequest{
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
			Qos:             autogen.QoS_RELIABLE,
			ExtensionFields: nil,
		},
	}}
	upstreamOpenRequestNilExtension = &message.UpstreamOpenRequest{
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
		QoS:             message.QoSReliable,
		ExtensionFields: nil,
	}
	upstreamOpenResponsePBNilExtension = &autogen.Message{Message: &autogen.Message_UpstreamOpenResponse{
		UpstreamOpenResponse: &autogen.UpstreamOpenResponse{
			RequestId:             1,
			AssignedStreamId:      mustMarshalBinary(uuid.MustParse("11111111-1111-1111-1111-111111111111")),
			AssignedStreamIdAlias: 2,
			ResultCode:            autogen.ResultCode_SUCCEEDED,
			ResultString:          "ResultString",
			ServerTime:            3,
			DataIdAliases: map[uint32]*autogen.DataID{
				1: {
					Name: "Name",
					Type: "Type",
				},
			},
			ExtensionFields: nil,
		},
	}}
	upstreamOpenResponseNilExtension = &message.UpstreamOpenResponse{
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
		ExtensionFields: nil,
	}
	upstreamResumeRequestPBNilExtension = &autogen.Message{Message: &autogen.Message_UpstreamResumeRequest{
		UpstreamResumeRequest: &autogen.UpstreamResumeRequest{
			RequestId:       1,
			StreamId:        mustMarshalBinary(uuid.MustParse("11111111-1111-1111-1111-111111111111")),
			ExtensionFields: nil,
		},
	}}
	upstreamResumeRequestNilExtension = &message.UpstreamResumeRequest{
		RequestID:       1,
		StreamID:        uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		ExtensionFields: nil,
	}
	upstreamResumeResponsePBNilExtension = &autogen.Message{Message: &autogen.Message_UpstreamResumeResponse{
		UpstreamResumeResponse: &autogen.UpstreamResumeResponse{
			RequestId:             1,
			AssignedStreamIdAlias: 2,
			ResultCode:            autogen.ResultCode_SUCCEEDED,
			ResultString:          "ResultString",
			ExtensionFields:       nil,
		},
	}}
	upstreamResumeResponseNilExtension = &message.UpstreamResumeResponse{
		RequestID:             1,
		AssignedStreamIDAlias: 2,
		ResultCode:            message.ResultCodeSucceeded,
		ResultString:          "ResultString",
		ExtensionFields:       nil,
	}
	upstreamCloseRequestPBNilExtension = &autogen.Message{Message: &autogen.Message_UpstreamCloseRequest{
		UpstreamCloseRequest: &autogen.UpstreamCloseRequest{
			RequestId:           1,
			StreamId:            mustMarshalBinary(uuid.MustParse("11111111-1111-1111-1111-111111111111")),
			TotalDataPoints:     2,
			FinalSequenceNumber: 3,
			ExtensionFields:     nil,
		},
	}}
	upstreamCloseRequestNilExtension = &message.UpstreamCloseRequest{
		RequestID:           1,
		StreamID:            uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		TotalDataPoints:     2,
		FinalSequenceNumber: 3,
		ExtensionFields:     nil,
	}
	upstreamCloseResponsePBNilExtension = &autogen.Message{Message: &autogen.Message_UpstreamCloseResponse{
		UpstreamCloseResponse: &autogen.UpstreamCloseResponse{
			RequestId:       1,
			ResultCode:      autogen.ResultCode_SUCCEEDED,
			ResultString:    "ResultString",
			ExtensionFields: nil,
		},
	}}
	upstreamCloseResponseNilExtension = &message.UpstreamCloseResponse{
		RequestID:       1,
		ResultCode:      message.ResultCodeSucceeded,
		ResultString:    "ResultString",
		ExtensionFields: nil,
	}

	upstreamDataPointsPBNilExtension = &autogen.Message{Message: &autogen.Message_UpstreamChunk{UpstreamChunk: &autogen.UpstreamChunk{
		StreamIdAlias: 1,
		DataIds:       []*autogen.DataID{},
		StreamChunk: &autogen.StreamChunk{
			SequenceNumber: 2,
			DataPointGroups: []*autogen.DataPointGroup{
				{
					DataIdOrAlias: &autogen.DataPointGroup_DataIdAlias{DataIdAlias: 3},
					DataPoints:    []*autogen.DataPoint{{ElapsedTime: 4, Payload: []byte{0, 1, 2, 3, 4}}},
				},
			},
		},
		ExtensionFields: nil,
	}}}
	upstreamDataPointsNilExtension = &message.UpstreamChunk{
		StreamIDAlias: 1,
		DataIDs:       []*message.DataID{},
		StreamChunk: &message.StreamChunk{
			SequenceNumber: 2,
			DataPointGroups: []*message.DataPointGroup{
				{
					DataIDOrAlias: message.DataIDAlias(3),
					DataPoints: []*message.DataPoint{
						{
							ElapsedTime: 4,
							Payload:     []byte{0, 1, 2, 3, 4},
						},
					},
				},
			},
		},
		ExtensionFields: nil,
	}
	upstreamDataPointsAckPBNilExtension = &autogen.Message{Message: &autogen.Message_UpstreamChunkAck{
		UpstreamChunkAck: &autogen.UpstreamChunkAck{
			StreamIdAlias: 1,
			Results: []*autogen.UpstreamChunkResult{{
				SequenceNumber:  1,
				ResultCode:      autogen.ResultCode_SUCCEEDED,
				ResultString:    "SUCCEEDED",
				ExtensionFields: nil,
			}},
			DataIdAliases: map[uint32]*autogen.DataID{
				1: {
					Name: "name",
					Type: "type",
				},
			},
			ExtensionFields: nil,
		},
	}}
	upstreamDataPointsAckNilExtension = &message.UpstreamChunkAck{
		StreamIDAlias: 1,
		Results: []*message.UpstreamChunkResult{
			{
				SequenceNumber:  1,
				ResultCode:      message.ResultCodeSucceeded,
				ResultString:    "SUCCEEDED",
				ExtensionFields: nil,
			},
		},
		DataIDAliases: map[uint32]*message.DataID{
			1: {
				Name: "name",
				Type: "type",
			},
		},
		ExtensionFields: nil,
	}
	downstreamDataPointsPBNilExtension = &autogen.Message{Message: &autogen.Message_DownstreamChunk{
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
			ExtensionFields: nil,
		},
	}}
	downstreamDataPointsNilExtension = &message.DownstreamChunk{
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
		ExtensionFields: nil,
	}
	downstreamDataPointsAckPBNilExtension = &autogen.Message{Message: &autogen.Message_DownstreamChunkAck{
		DownstreamChunkAck: &autogen.DownstreamChunkAck{
			StreamIdAlias: 1,
			AckId:         2,
			Results: []*autogen.DownstreamChunkResult{{
				SequenceNumberInUpstream: 2,
				StreamIdOfUpstream:       mustMarshalBinary(uuid.MustParse("11111111-1111-1111-1111-111111111111")),
				ResultCode:               autogen.ResultCode_SUCCEEDED,
				ResultString:             "SUCCEEDED",
				ExtensionFields:          nil,
			}},
			UpstreamAliases: map[uint32]*autogen.UpstreamInfo{},
			DataIdAliases:   map[uint32]*autogen.DataID{},
			ExtensionFields: nil,
		},
	}}

	downstreamDataPointsAckNilExtension = &message.DownstreamChunkAck{
		StreamIDAlias: 1,
		AckID:         2,
		Results: []*message.DownstreamChunkResult{
			{
				SequenceNumberInUpstream: 2,
				StreamIDOfUpstream:       uuid.MustParse("11111111-1111-1111-1111-111111111111"),
				ResultCode:               message.ResultCodeSucceeded,
				ResultString:             "SUCCEEDED",
				ExtensionFields:          nil,
			},
		},
		UpstreamAliases: map[uint32]*message.UpstreamInfo{},
		DataIDAliases:   map[uint32]*message.DataID{},
		ExtensionFields: nil,
	}

	downstreamDataPointsAckCompletePBNilExtension = &autogen.Message{
		Message: &autogen.Message_DownstreamChunkAckComplete{
			DownstreamChunkAckComplete: &autogen.DownstreamChunkAckComplete{
				StreamIdAlias:   1,
				AckId:           2,
				ResultCode:      autogen.ResultCode_SUCCEEDED,
				ResultString:    "SUCCEEDED",
				ExtensionFields: nil,
			},
		},
	}
	downstreamDataPointsAckCompleteNilExtension = &message.DownstreamChunkAckComplete{
		StreamIDAlias:   1,
		AckID:           2,
		ResultCode:      message.ResultCodeSucceeded,
		ResultString:    "SUCCEEDED",
		ExtensionFields: nil,
	}
)

// metadata proto
var (
	downstreamMetadataBaseTimePBNilExtension = &autogen.DownstreamMetadata{
		RequestId:    1,
		SourceNodeId: "SourceNodeId",
		Metadata: &autogen.DownstreamMetadata_BaseTime{BaseTime: &autogen.BaseTime{
			SessionId:   "SessionId",
			Name:        "Name",
			Priority:    1,
			ElapsedTime: 2,
			BaseTime:    3,
		}},
		ExtensionFields: nil,
	}
	upstreamMetadataBaseTimePBNilExtension = &autogen.UpstreamMetadata{
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
		ExtensionFields: nil,
	}
	metadataBaseTimeNilExtension = &message.BaseTime{
		SessionID:   "SessionId",
		Name:        "Name",
		Priority:    1,
		ElapsedTime: 2,
		BaseTime:    time.Unix(0, 3).UTC(),
	}
	downstreamMetadataUpstreamOpenPBNilExtension = &autogen.DownstreamMetadata{
		RequestId:       1,
		SourceNodeId:    "SourceNodeId",
		Metadata:        &autogen.DownstreamMetadata_UpstreamOpen{UpstreamOpen: &autogen.UpstreamOpen{StreamId: mustMarshalBinary(uuid.MustParse("11111111-1111-1111-1111-111111111111")), SessionId: "SessionId", Qos: autogen.QoS_RELIABLE}},
		ExtensionFields: nil,
	}
	metadataUpstreamOpenNilExtension = &message.UpstreamOpen{
		StreamID:  uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		SessionID: "SessionId",
		QoS:       message.QoSReliable,
	}
	downstreamMetadataUpstreamAbnormalClosePBNilExtension = &autogen.DownstreamMetadata{
		RequestId:       1,
		SourceNodeId:    "SourceNodeId",
		Metadata:        &autogen.DownstreamMetadata_UpstreamAbnormalClose{UpstreamAbnormalClose: &autogen.UpstreamAbnormalClose{StreamId: mustMarshalBinary(uuid.MustParse("11111111-1111-1111-1111-111111111111")), SessionId: "SessionId"}},
		ExtensionFields: nil,
	}
	metadataUpstreamAbnormalCloseNilExtension = &message.UpstreamAbnormalClose{
		StreamID:  uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		SessionID: "SessionId",
	}
	downstreamMetadataUpstreamResumePBNilExtension = &autogen.DownstreamMetadata{
		RequestId:       1,
		SourceNodeId:    "SourceNodeId",
		Metadata:        &autogen.DownstreamMetadata_UpstreamResume{UpstreamResume: &autogen.UpstreamResume{StreamId: mustMarshalBinary(uuid.MustParse("11111111-1111-1111-1111-111111111111")), SessionId: "SessionId"}},
		ExtensionFields: nil,
	}
	metadataUpstreamResumeNilExtension = &message.UpstreamResume{
		StreamID:  uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		SessionID: "SessionId",
	}
	downstreamMetadataUpstreamNormalClosePBNilExtension = &autogen.DownstreamMetadata{
		RequestId:       1,
		SourceNodeId:    "SourceNodeId",
		Metadata:        &autogen.DownstreamMetadata_UpstreamNormalClose{UpstreamNormalClose: &autogen.UpstreamNormalClose{StreamId: mustMarshalBinary(uuid.MustParse("11111111-1111-1111-1111-111111111111")), TotalDataPoints: 1, FinalSequenceNumber: 2}},
		ExtensionFields: nil,
	}
	metadataUpstreamNormalCloseNilExtension = &message.UpstreamNormalClose{
		StreamID:            uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		TotalDataPoints:     1,
		FinalSequenceNumber: 2,
	}
	downstreamMetadataDownstreamOpenPBNilExtension = &autogen.DownstreamMetadata{
		RequestId:    1,
		SourceNodeId: "SourceNodeId",
		Metadata: &autogen.DownstreamMetadata_DownstreamOpen{DownstreamOpen: &autogen.DownstreamOpen{StreamId: mustMarshalBinary(uuid.MustParse("11111111-1111-1111-1111-111111111111")), DownstreamFilters: []*autogen.DownstreamFilter{{
			SourceNodeId: "SourceNodeId",
			DataFilters:  []*autogen.DataFilter{{Name: "Name", Type: "Type"}},
		}}, Qos: autogen.QoS_RELIABLE}},
		ExtensionFields: nil,
	}
	metadataDownstreamOpenNilExtension = &message.DownstreamOpen{
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
	downstreamMetadataDownstreamAbnormalClosePBNilExtension = &autogen.DownstreamMetadata{
		RequestId:       1,
		SourceNodeId:    "SourceNodeId",
		Metadata:        &autogen.DownstreamMetadata_DownstreamAbnormalClose{DownstreamAbnormalClose: &autogen.DownstreamAbnormalClose{StreamId: mustMarshalBinary(uuid.MustParse("11111111-1111-1111-1111-111111111111"))}},
		ExtensionFields: nil,
	}
	metadataDownstreamAbnormalCloseNilExtension = &message.DownstreamAbnormalClose{
		StreamID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
	}
	downstreamMetadataDownstreamResumePBNilExtension = &autogen.DownstreamMetadata{
		RequestId:       1,
		SourceNodeId:    "SourceNodeId",
		Metadata:        &autogen.DownstreamMetadata_DownstreamResume{DownstreamResume: &autogen.DownstreamResume{StreamId: mustMarshalBinary(uuid.MustParse("11111111-1111-1111-1111-111111111111"))}},
		ExtensionFields: nil,
	}
	metadataDownstreamResumeNilExtension = &message.DownstreamResume{
		StreamID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
	}
	downstreamMetadataDownstreamNormalClosePBNilExtension = &autogen.DownstreamMetadata{
		RequestId:    1,
		SourceNodeId: "SourceNodeId",
		Metadata: &autogen.DownstreamMetadata_DownstreamNormalClose{DownstreamNormalClose: &autogen.DownstreamNormalClose{
			StreamId: mustMarshalBinary(uuid.MustParse("11111111-1111-1111-1111-111111111111")),
		}},
		ExtensionFields: nil,
	}
	metadataDownstreamNormalCloseNilExtension = &message.DownstreamNormalClose{
		StreamID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
	}
)
