package json_test

import (
	"time"

	uuid "github.com/google/uuid"

	"github.com/aptpod/iscp-go/message"
)

var (
	ping = &message.Ping{
		RequestID:       1,
		ExtensionFields: &message.PingExtensionFields{},
	}
	pong = &message.Pong{
		RequestID:       1,
		ExtensionFields: &message.PongExtensionFields{},
	}
	connectRequest = &message.ConnectRequest{
		RequestID:       1,
		ProtocolVersion: "ProtocolVersion",
		NodeID:          "NodeId",
		PingTimeout:     2 * time.Second,
		PingInterval:    3 * time.Second,
		ExtensionFields: &message.ConnectRequestExtensionFields{
			AccessToken: "AccessToken",
			Intdash: &message.IntdashExtensionFields{
				ProjectUUID: uuid.MustParse("3b75684d-751b-4bf0-b12d-c7510a327891"),
			},
		},
	}
	connectResponse = &message.ConnectResponse{
		RequestID:       1,
		ProtocolVersion: "ProtocolVersion",
		ResultCode:      message.ResultCodeSucceeded,
		ResultString:    "ResultString",
		ExtensionFields: &message.ConnectResponseExtensionFields{},
	}
	disconnect = &message.Disconnect{
		ResultCode:      message.ResultCodeSucceeded,
		ResultString:    "ResultString",
		ExtensionFields: &message.DisconnectExtensionFields{},
	}
	downstreamOpenRequest = &message.DownstreamOpenRequest{
		RequestID:            1,
		DesiredStreamIDAlias: 2,
		DownstreamFilters:    []*message.DownstreamFilter{{SourceNodeID: "SourceNodeId", DataFilters: []*message.DataFilter{{Name: "Name", Type: "Type"}}}},
		ExpiryInterval:       3 * time.Second,
		DataIDAliases:        map[uint32]*message.DataID{1: {Name: "Name", Type: "Type"}},
		QoS:                  message.QoSReliable,
		ExtensionFields:      &message.DownstreamOpenRequestExtensionFields{},
		OmitEmptyChunk:       true,
	}
	downstreamOpenResponse = &message.DownstreamOpenResponse{
		RequestID:        1,
		AssignedStreamID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		ServerTime:       time.Unix(1, 0).UTC(),
		ResultCode:       message.ResultCodeSucceeded,
		ResultString:     "ResultString",
		ExtensionFields:  &message.DownstreamOpenResponseExtensionFields{},
	}
	downstreamResumeRequest = &message.DownstreamResumeRequest{
		RequestID:            1,
		StreamID:             uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		DesiredStreamIDAlias: 2,
		ExtensionFields:      &message.DownstreamResumeRequestExtensionFields{},
	}
	downstreamResumeResponse = &message.DownstreamResumeResponse{
		RequestID:       1,
		ResultCode:      message.ResultCodeSucceeded,
		ResultString:    "ResultString",
		ExtensionFields: &message.DownstreamResumeResponseExtensionFields{},
	}
	downstreamCloseRequest = &message.DownstreamCloseRequest{
		RequestID:       1,
		StreamID:        uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		ExtensionFields: &message.DownstreamCloseRequestExtensionFields{},
	}
	downstreamCloseResponse = &message.DownstreamCloseResponse{
		RequestID:       1,
		ResultCode:      message.ResultCodeSucceeded,
		ResultString:    "ResultString",
		ExtensionFields: &message.DownstreamCloseResponseExtensionFields{},
	}
	upstreamCall = &message.UpstreamCall{
		CallID:            "CallId",
		RequestCallID:     "RequestCallId",
		DestinationNodeID: "DestinationNodeId",
		Name:              "Name",
		Type:              "Type",
		Payload:           []byte("payload"),
		ExtensionFields:   &message.UpstreamCallExtensionFields{},
	}
	downstreamCall = &message.DownstreamCall{
		CallID:          "CallId",
		RequestCallID:   "RequestCallId",
		SourceNodeID:    "SourceNodeId",
		Name:            "Name",
		Type:            "Type",
		Payload:         []byte("payload"),
		ExtensionFields: &message.DownstreamCallExtensionFields{},
	}
	upstreamMetadata = &message.UpstreamMetadata{
		RequestID: 1,
		Metadata: &message.BaseTime{
			SessionID:   "",
			Name:        "Name",
			Priority:    1,
			ElapsedTime: 2,
			BaseTime:    time.Unix(0, 3).UTC(),
		},
		ExtensionFields: &message.UpstreamMetadataExtensionFields{
			Persist: true,
		},
	}
	upstreamMetadataAck = &message.UpstreamMetadataAck{
		RequestID:       1,
		ResultCode:      message.ResultCodeSucceeded,
		ResultString:    "ResultString",
		ExtensionFields: &message.UpstreamMetadataAckExtensionFields{},
	}
	downstreamMetadata = &message.DownstreamMetadata{
		RequestID:    1,
		SourceNodeID: "",
		Metadata: &message.BaseTime{
			SessionID:   "",
			Name:        "Name",
			Priority:    1,
			ElapsedTime: 2,
			BaseTime:    time.Unix(0, 3).UTC(),
		},
		ExtensionFields: &message.DownstreamMetadataExtensionFields{},
	}
	downstreamMetadataAck = &message.DownstreamMetadataAck{
		RequestID:       1,
		ResultCode:      message.ResultCodeSucceeded,
		ResultString:    "ResultString",
		ExtensionFields: &message.DownstreamMetadataAckExtensionFields{},
	}
	upstreamOpenRequest = &message.UpstreamOpenRequest{
		RequestID:      1,
		SessionID:      "SessionId",
		AckInterval:    2 * time.Millisecond,
		ExpiryInterval: 4 * time.Second,
		DataIDs:        []*message.DataID{{Name: "Name", Type: "Type"}},
		QoS:            message.QoSReliable,
		ExtensionFields: &message.UpstreamOpenRequestExtensionFields{
			Persist: true,
		},
	}
	upstreamOpenResponse = &message.UpstreamOpenResponse{
		RequestID:             1,
		AssignedStreamID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		AssignedStreamIDAlias: 2,
		ResultCode:            message.ResultCodeSucceeded,
		ResultString:          "ResultString",
		ServerTime:            time.Unix(0, 3).UTC(),
		DataIDAliases:         map[uint32]*message.DataID{1: {Name: "Name", Type: "Type"}},
		ExtensionFields:       &message.UpstreamOpenResponseExtensionFields{},
	}
	upstreamResumeRequest = &message.UpstreamResumeRequest{
		RequestID:       1,
		StreamID:        uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		ExtensionFields: &message.UpstreamResumeRequestExtensionFields{},
	}
	upstreamResumeResponse = &message.UpstreamResumeResponse{
		RequestID:             1,
		AssignedStreamIDAlias: 2,
		ResultCode:            message.ResultCodeSucceeded,
		ResultString:          "ResultString",
		ExtensionFields:       &message.UpstreamResumeResponseExtensionFields{},
	}
	upstreamCloseRequest = &message.UpstreamCloseRequest{
		RequestID:           1,
		StreamID:            uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		TotalDataPoints:     2,
		FinalSequenceNumber: 3,
		ExtensionFields:     &message.UpstreamCloseRequestExtensionFields{},
	}
	upstreamCloseResponse = &message.UpstreamCloseResponse{
		RequestID:       1,
		ResultCode:      message.ResultCodeSucceeded,
		ResultString:    "ResultString",
		ExtensionFields: &message.UpstreamCloseResponseExtensionFields{},
	}

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
		DataIDs:         []*message.DataID{{Name: "Name", Type: "Type"}},
		ExtensionFields: &message.UpstreamChunkExtensionFields{},
	}
	upstreamDataPointsAck = &message.UpstreamChunkAck{
		StreamIDAlias: 1,
		Results: []*message.UpstreamChunkResult{{
			SequenceNumber:  1,
			ResultCode:      message.ResultCodeSucceeded,
			ResultString:    "SUCCEEDED",
			ExtensionFields: &message.UpstreamChunkResultExtensionFields{},
		}},
		ExtensionFields: &message.UpstreamChunkAckExtensionFields{},
		DataIDAliases:   map[uint32]*message.DataID{},
	}
	downstreamDataPoints = &message.DownstreamChunk{
		StreamIDAlias:   1,
		UpstreamOrAlias: message.UpstreamAlias(2),
		StreamChunk: &message.StreamChunk{
			SequenceNumber: 3,
			DataPointGroups: []*message.DataPointGroup{
				{
					DataIDOrAlias: message.DataIDAlias(4),
					DataPoints:    []*message.DataPoint{{ElapsedTime: 5, Payload: []byte{0, 1, 2, 3, 4}}},
				},
			},
		},
		DownstreamFilterReferences: [][]*message.DownstreamFilterReference{},
		ExtensionFields:            &message.DownstreamChunkExtensionFields{},
	}
	downstreamDataPointsAck = &message.DownstreamChunkAck{
		StreamIDAlias: 1,
		AckID:         0,
		Results: []*message.DownstreamChunkResult{{
			StreamIDOfUpstream:       uuid.MustParse("22222222-2222-2222-2222-222222222222"),
			SequenceNumberInUpstream: 2,
			ResultCode:               message.ResultCodeSucceeded,
			ResultString:             "SUCCEEDED",
			ExtensionFields:          &message.DownstreamChunkResultExtensionFields{},
		}},
		UpstreamAliases: map[uint32]*message.UpstreamInfo{
			1: {
				SessionID:    "SessionID",
				SourceNodeID: "SourceNodeID",
				StreamID:     uuid.MustParse("22222222-2222-2222-2222-222222222222"),
			},
		},
		DataIDAliases:   map[uint32]*message.DataID{},
		ExtensionFields: &message.DownstreamChunkAckExtensionFields{},
	}
	downstreamDataPointsAckComplete = &message.DownstreamChunkAckComplete{
		StreamIDAlias:   1,
		AckID:           2,
		ResultCode:      message.ResultCodeSucceeded,
		ResultString:    "SUCCEEDED",
		ExtensionFields: &message.DownstreamChunkAckCompleteExtensionFields{},
	}
)
