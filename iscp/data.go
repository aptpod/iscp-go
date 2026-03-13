package iscp

import (
	"github.com/aptpod/iscp-go/message"
)

// DataPointsは、複数のデータポイントです。
type DataPoints []*message.DataPoint

func (ps DataPoints) withoutPayload() DataPoints {
	res := make(DataPoints, 0, len(ps))
	for _, v := range ps {
		res = append(res, &message.DataPoint{
			ElapsedTime: v.ElapsedTime,
		})
	}
	return res
}

// DataPointGroupは、データポイントグループです。
type DataPointGroup struct {
	// データID
	DataID *message.DataID

	// データポイント
	DataPoints DataPoints
}

func (dpg *DataPointGroup) payloadSize() int {
	var size int
	for _, v := range dpg.DataPoints {
		size += len(v.Payload)
	}
	return size
}

// DataPointGroupsは、複数のデータポイントグループです。
type DataPointGroups []*DataPointGroup

func (dpgs DataPointGroups) toUpstreamDataPointGroups(revAliases map[message.DataID]uint32) ([]*message.DataPointGroup, []*message.DataID) {
	res := make([]*message.UpstreamDataPointGroup, 0)
	resIDs := make([]*message.DataID, 0)
	alreadyAppended := map[message.DataID]struct{}{}

	for _, dpg := range dpgs {
		alias, ok := revAliases[*dpg.DataID]
		var mdpg message.DataPointGroup
		if ok {
			mdpg = message.DataPointGroup{
				DataIDOrAlias: message.DataIDAlias(alias),
				DataPoints:    []*message.DataPoint{},
			}
		} else {
			mdpg = message.DataPointGroup{
				DataIDOrAlias: dpg.DataID,
				DataPoints:    []*message.DataPoint{},
			}
			if _, ok := alreadyAppended[*dpg.DataID]; !ok {
				resIDs = append(resIDs, dpg.DataID)
				alreadyAppended[*dpg.DataID] = struct{}{}
			}
		}
		mdpg.DataPoints = append(mdpg.DataPoints, dpg.DataPoints...)
		res = append(res, &mdpg)
	}

	return res, resIDs
}

func (dpgs DataPointGroups) withoutPayload() DataPointGroups {
	res := make(DataPointGroups, 0, len(dpgs))
	for _, dpg := range dpgs {
		res = append(res, &DataPointGroup{
			DataID:     dpg.DataID,
			DataPoints: dpg.DataPoints.withoutPayload(),
		})
	}
	return res
}

// UpstreamChunkは、アップストリームで送信するデータポイントです。
type UpstreamChunk struct {
	// シーケンス番号
	SequenceNumber uint32
	// データポイントグループ
	DataPointGroups DataPointGroups
}

// UpstreamChunkResultは、UpstreamChunkの処理結果です。
type UpstreamChunkResult struct {
	// シーケンス番号
	SequenceNumber uint32
	// 結果コード
	ResultCode message.ResultCode
	// 結果文字列
	ResultString string
}

// DownstreamChunkは、ダウンストリームで取得したデータポイントです。
type DownstreamChunk struct {
	// シーケンス番号
	SequenceNumber uint32
	// データポイントグループ
	DataPointGroups DataPointGroups
	// アップストリーム情報
	UpstreamInfo *message.UpstreamInfo
	// ダウンストリームフィルターリファレンス
	DownstreamFilterReferences [][]*message.DownstreamFilterReference
}

// DownstreamMetadataは、ダウンストリームで取得したメタデータです。
type DownstreamMetadata struct {
	// 送信元ノードID
	SourceNodeID string
	// メタデータ
	Metadata message.Metadata
}

// UpstreamCallは、E2Eのアップストリームコールです。
type UpstreamCall struct {
	DestinationNodeID string // ノードID
	Name              string // 名称
	Type              string // 型
	Payload           []byte // ペイロード
}

// UpstreamReplyCallは、E2Eのリプライ用のコールです。
type UpstreamReplyCall struct {
	RequestCallID     string // 受信したDownstreamCallのコールID
	DestinationNodeID string // リプライ先のNodeID
	Name              string // 名称
	Type              string // 型
	Payload           []byte // ペイロード
}

// DownstreamCallは、E2Eのダウンストリームコールです。
type DownstreamCall struct {
	CallID       string // コールID
	SourceNodeID string // 送信元ノードID
	Name         string // 名称
	Type         string // 型
	Payload      []byte // ペイロード
}

// DownstreamReplyCallは、E2Eのリプライ用のコールです。
type DownstreamReplyCall struct {
	CallID        string // コールID
	RequestCallID string // リクエストコールID
	SourceNodeID  string // リプライコールの送信元ノードID
	Name          string // 名称
	Type          string // 型
	Payload       []byte // ペイロード
}
