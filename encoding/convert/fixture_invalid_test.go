package convert_test

import (
	"encoding"
	"math"

	autogen "github.com/aptpod/iscp-proto/gen/gogofast/iscp2/v1"
	uuid "github.com/google/uuid"

	"github.com/aptpod/iscp-go/message"
)

func mustMarshalBinary(in encoding.BinaryMarshaler) []byte {
	res, err := in.MarshalBinary()
	if err != nil {
		panic(err)
	}
	return res
}

// invalid proto
var (
	connectResponsePBInvalid = &autogen.Message{Message: &autogen.Message_ConnectResponse{
		ConnectResponse: &autogen.ConnectResponse{
			// invalid
			ResultCode: math.MaxInt32,
		},
	}}
	disconnectPBInvalid = &autogen.Message{Message: &autogen.Message_Disconnect{
		Disconnect: &autogen.Disconnect{
			ResultCode: math.MaxInt32,
		},
	}}
	upstreamOpenRequestPBInvalidQoS = &autogen.Message{Message: &autogen.Message_UpstreamOpenRequest{
		UpstreamOpenRequest: &autogen.UpstreamOpenRequest{
			Qos: math.MaxInt32,
		},
	}}
	upstreamOpenResponsePBInvalidUUID = &autogen.Message{Message: &autogen.Message_UpstreamOpenResponse{
		UpstreamOpenResponse: &autogen.UpstreamOpenResponse{
			// invalid
			AssignedStreamId: []byte{0, 1, 2, 3, 4},
		},
	}}
	upstreamOpenResponsePBInvalidResultCode = &autogen.Message{Message: &autogen.Message_UpstreamOpenResponse{
		UpstreamOpenResponse: &autogen.UpstreamOpenResponse{
			AssignedStreamId: mustMarshalBinary(uuid.MustParse("11111111-1111-1111-1111-111111111111")),
			// invalid
			ResultCode: math.MaxInt32,
		},
	}}
	upstreamResumeRequestPBInvalidUUID = &autogen.Message{Message: &autogen.Message_UpstreamResumeRequest{
		UpstreamResumeRequest: &autogen.UpstreamResumeRequest{
			StreamId: []byte{0, 1, 2, 3, 4},
		},
	}}
	upstreamResumeResponsePBInvalidResultCode = &autogen.Message{Message: &autogen.Message_UpstreamResumeResponse{
		UpstreamResumeResponse: &autogen.UpstreamResumeResponse{
			ResultCode: math.MaxInt32,
		},
	}}

	upstreamCloseRequestPBInvalidUUID = &autogen.Message{Message: &autogen.Message_UpstreamCloseRequest{
		UpstreamCloseRequest: &autogen.UpstreamCloseRequest{
			StreamId: []byte{0, 1, 2, 3, 4},
		},
	}}
	upstreamCloseResponsePBInvalidResultCode = &autogen.Message{Message: &autogen.Message_UpstreamCloseResponse{
		UpstreamCloseResponse: &autogen.UpstreamCloseResponse{
			ResultCode: math.MaxInt32,
		},
	}}

	downstreamOpenRequestPBInvalidQoS = &autogen.Message{Message: &autogen.Message_DownstreamOpenRequest{
		DownstreamOpenRequest: &autogen.DownstreamOpenRequest{
			Qos: math.MaxInt32,
		},
	}}
	downstreamOpenResponsePBInvalidUUID = &autogen.Message{Message: &autogen.Message_DownstreamOpenResponse{
		DownstreamOpenResponse: &autogen.DownstreamOpenResponse{
			AssignedStreamId: []byte{0, 1, 2, 3, 4},
			ResultCode:       autogen.ResultCode_SUCCEEDED,
		},
	}}
	downstreamOpenResponsePBInvalidResultCode = &autogen.Message{Message: &autogen.Message_DownstreamOpenResponse{
		DownstreamOpenResponse: &autogen.DownstreamOpenResponse{
			ResultCode: math.MaxInt32,
		},
	}}
	downstreamResumeRequestPBInvalidUUID = &autogen.Message{Message: &autogen.Message_DownstreamResumeRequest{
		DownstreamResumeRequest: &autogen.DownstreamResumeRequest{
			StreamId: []byte{0, 1, 2, 3, 4},
		},
	}}
	downstreamResumeResponsePBInvalidResultCode = &autogen.Message{Message: &autogen.Message_DownstreamResumeResponse{
		DownstreamResumeResponse: &autogen.DownstreamResumeResponse{
			ResultCode: math.MaxInt32,
		},
	}}

	downstreamCloseRequestPBInvalidUUID = &autogen.Message{Message: &autogen.Message_DownstreamCloseRequest{
		DownstreamCloseRequest: &autogen.DownstreamCloseRequest{
			StreamId: []byte{0, 1, 3, 4},
		},
	}}
	downstreamCloseResponsePBInvalidResultCode = &autogen.Message{Message: &autogen.Message_DownstreamCloseResponse{
		DownstreamCloseResponse: &autogen.DownstreamCloseResponse{
			ResultCode: math.MaxInt32,
		},
	}}

	upstreamDataPointsAckPBInvalidResultCode = &autogen.Message{Message: &autogen.Message_UpstreamChunkAck{
		UpstreamChunkAck: &autogen.UpstreamChunkAck{
			Results: []*autogen.UpstreamChunkResult{
				{
					ResultCode: math.MaxInt32,
				},
			},
		},
	}}

	downstreamDataPointsPBInvalidUUID = &autogen.Message{Message: &autogen.Message_DownstreamChunk{
		DownstreamChunk: &autogen.DownstreamChunk{
			UpstreamOrAlias: &autogen.DownstreamChunk_UpstreamInfo{
				UpstreamInfo: &autogen.UpstreamInfo{
					StreamId: []byte{0, 1, 2, 3, 4},
				},
			},
		},
	}}

	downstreamDataPointsAckPBInvalidResultCode = &autogen.Message{Message: &autogen.Message_DownstreamChunkAck{
		DownstreamChunkAck: &autogen.DownstreamChunkAck{
			Results: []*autogen.DownstreamChunkResult{
				{
					ResultCode: math.MaxInt32,
				},
			},
		},
	}}

	upstreamMetadataAckPBInvalidResultCode = &autogen.Message{Message: &autogen.Message_UpstreamMetadataAck{
		UpstreamMetadataAck: &autogen.UpstreamMetadataAck{
			ResultCode: math.MaxInt32,
		},
	}}

	downstreamMetadataAckPBInvalidResultCode = &autogen.Message{Message: &autogen.Message_DownstreamMetadataAck{
		DownstreamMetadataAck: &autogen.DownstreamMetadataAck{
			ResultCode: math.MaxInt32,
		},
	}}
	downstreamMetadataPBInvalidUpstreamOpenUUID = &autogen.Message{Message: &autogen.Message_DownstreamMetadata{
		DownstreamMetadata: &autogen.DownstreamMetadata{
			Metadata: &autogen.DownstreamMetadata_UpstreamOpen{
				UpstreamOpen: &autogen.UpstreamOpen{
					StreamId: []byte{0, 1, 2, 3, 4},
				},
			},
		},
	}}
	downstreamMetadataPBInvalidUpstreamQoS = &autogen.Message{Message: &autogen.Message_DownstreamMetadata{
		DownstreamMetadata: &autogen.DownstreamMetadata{
			Metadata: &autogen.DownstreamMetadata_UpstreamOpen{
				UpstreamOpen: &autogen.UpstreamOpen{
					StreamId: mustMarshalBinary(uuid.MustParse("11111111-1111-1111-1111-111111111111")),
					Qos:      math.MaxInt32,
				},
			},
		},
	}}

	downstreamMetadataPBInvalidUpstreamAbnormalClose = &autogen.Message{Message: &autogen.Message_DownstreamMetadata{DownstreamMetadata: &autogen.DownstreamMetadata{
		Metadata: &autogen.DownstreamMetadata_UpstreamAbnormalClose{
			UpstreamAbnormalClose: &autogen.UpstreamAbnormalClose{
				StreamId: []byte{0, 1, 2, 3, 4},
			},
		},
	}}}
	downstreamMetadataPBInvalidUpstreamResume = &autogen.Message{Message: &autogen.Message_DownstreamMetadata{DownstreamMetadata: &autogen.DownstreamMetadata{
		Metadata: &autogen.DownstreamMetadata_UpstreamResume{
			UpstreamResume: &autogen.UpstreamResume{
				StreamId: []byte{0, 1, 2, 3, 4},
			},
		},
	}}}
	downstreamMetadataPBInvalidUpstreamNormalClose = &autogen.Message{Message: &autogen.Message_DownstreamMetadata{DownstreamMetadata: &autogen.DownstreamMetadata{
		Metadata: &autogen.DownstreamMetadata_UpstreamNormalClose{
			UpstreamNormalClose: &autogen.UpstreamNormalClose{
				StreamId: []byte{0, 1, 2, 3, 4},
			},
		},
	}}}
	downstreamMetadataPBInvalidDownstreamOpenQoS = &autogen.Message{Message: &autogen.Message_DownstreamMetadata{DownstreamMetadata: &autogen.DownstreamMetadata{
		Metadata: &autogen.DownstreamMetadata_DownstreamOpen{
			DownstreamOpen: &autogen.DownstreamOpen{
				StreamId: mustMarshalBinary(uuid.MustParse("11111111-1111-1111-1111-111111111111")),
				Qos:      math.MaxInt32,
			},
		},
	}}}
	downstreamMetadataPBInvalidDownstreamOpenUUID = &autogen.Message{Message: &autogen.Message_DownstreamMetadata{DownstreamMetadata: &autogen.DownstreamMetadata{
		RequestId: 0,
		Metadata: &autogen.DownstreamMetadata_DownstreamOpen{
			DownstreamOpen: &autogen.DownstreamOpen{
				StreamId: []byte{0, 1, 2, 3, 4},
				Qos:      autogen.QoS_RELIABLE,
			},
		},
	}}}
	downstreamMetadataPBInvalidDownstreamAbnormalClose = &autogen.Message{Message: &autogen.Message_DownstreamMetadata{DownstreamMetadata: &autogen.DownstreamMetadata{
		Metadata: &autogen.DownstreamMetadata_DownstreamAbnormalClose{
			DownstreamAbnormalClose: &autogen.DownstreamAbnormalClose{
				StreamId: []byte{0, 1, 2, 3, 4},
			},
		},
	}}}
	downstreamMetadataPBInvalidDownstreamResume = &autogen.Message{Message: &autogen.Message_DownstreamMetadata{DownstreamMetadata: &autogen.DownstreamMetadata{
		Metadata: &autogen.DownstreamMetadata_DownstreamResume{
			DownstreamResume: &autogen.DownstreamResume{
				StreamId: []byte{0, 1, 2, 3, 4},
			},
		},
	}}}
	downstreamMetadataPBInvalidDownstreamNormalClose = &autogen.Message{Message: &autogen.Message_DownstreamMetadata{DownstreamMetadata: &autogen.DownstreamMetadata{
		Metadata: &autogen.DownstreamMetadata_DownstreamNormalClose{
			DownstreamNormalClose: &autogen.DownstreamNormalClose{
				StreamId: []byte{0, 1, 2, 3, 4},
			},
		},
	}}}
)

// invalid message
var (
	connectResponseInvalid = &message.ConnectResponse{
		ResultCode: math.MaxUint8,
	}
	disconnectInvalid = &message.Disconnect{
		ResultCode: math.MaxUint8,
	}
	upstreamOpenRequestInvalidQoS             = &message.UpstreamOpenRequest{QoS: math.MaxUint8}
	upstreamOpenResponseInvalidUUID           = &message.UpstreamOpenResponse{ResultCode: math.MaxUint8}
	upstreamOpenResponseInvalidResultCode     = &message.UpstreamOpenResponse{ResultCode: math.MaxUint8}
	upstreamResumeResponseInvalidResultCode   = &message.UpstreamResumeResponse{ResultCode: math.MaxUint8}
	upstreamCloseResponseInvalidResultCode    = &message.UpstreamCloseResponse{ResultCode: math.MaxUint8}
	downstreamOpenRequestInvalidQoS           = &message.DownstreamOpenRequest{QoS: math.MaxUint8}
	downstreamOpenResponseInvalidResultCode   = &message.DownstreamOpenResponse{ResultCode: math.MaxUint8}
	downstreamResumeResponseInvalidResultCode = &message.DownstreamResumeResponse{ResultCode: math.MaxUint8}
	downstreamCloseResponseInvalidResultCode  = &message.DownstreamCloseResponse{ResultCode: math.MaxUint8}
	upstreamDataPointsAckInvalidResultCode    = &message.UpstreamChunkAck{
		Results: []*message.UpstreamChunkResult{
			{
				ResultCode: math.MaxUint8,
			},
		},
	}
	downstreamDataPointsAckInvalidResultCode = &message.DownstreamChunkAck{
		Results: []*message.DownstreamChunkResult{
			{
				ResultCode: math.MaxUint8,
			},
		},
	}
	upstreamMetadataAckInvalidResultCode       = &message.UpstreamMetadataAck{ResultCode: math.MaxUint8}
	downstreamMetadataAckInvalidResultCode     = &message.DownstreamMetadataAck{ResultCode: math.MaxUint8}
	downstreamMetadataInvalidUpstreamQoS       = &message.DownstreamMetadata{Metadata: &message.UpstreamOpen{QoS: math.MaxUint8}}
	downstreamMetadataInvalidDownstreamOpenQoS = &message.DownstreamMetadata{Metadata: &message.DownstreamOpen{QoS: math.MaxUint8}}
)
