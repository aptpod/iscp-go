package message

import (
	"fmt"
	"strings"
)

// DownstreamFilterは、ダウンストリームフィルタです。
//
// ダウンストリームフィルタはダウンストリームにおいてブローカーからノードに送信するデータを特定します。
type DownstreamFilter struct {
	SourceNodeID string        // 送信元ノードID
	DataFilters  []*DataFilter // データフィルタ
}

// TODO:
func NewDownstreamFilterAllFor(sourceNodeID string) *DownstreamFilter {
	return &DownstreamFilter{
		SourceNodeID: sourceNodeID,
		DataFilters: []*DataFilter{
			{
				Name: "#",
				Type: "#",
			},
		},
	}
}

// DataFilterは、データフィルタです。
//
// データフィルタは受信対象とするデータを表します。
// 特殊文字 `/` はセパレータです。名称/型の階層構造を表現することができます。
// 特殊文字 `#`, はマルチレベルワイルドカードです。
// 例） フィルター、文字列、マッチするか
// - `#,name,true`
// - `#,group/name,true`
// - `group/#,group/name,true`
// - `group/#,group/sub-group/name,true`
// - `group/#,other-group/name,false`

// 特殊文字 `+`, は単一レベルワイルドカードです。

// 例） フィルター、文字列、マッチするか
// - `+,name,true`
// - `group/+,group/name,true`
// - `group/+/name,group/sub-group/name,true`
// - `group/+/name,group/other-group/name,true`
// - `group/+/name,group/other-group/some-name,false`

type DataFilter struct {
	Name string // 名称
	Type string // 型
}

func (d DataFilter) String() string {
	return fmt.Sprintf("%s:%s", d.Type, d.Name)
}

func (d *DataFilter) UnmarshalText(src []byte) error {
	sp := strings.Split(string(src), ":")
	if len(sp) != 2 {
		return fmt.Errorf("invalid data_id:[%s]", string(src))
	}
	d.Type = sp[0]
	d.Name = sp[1]
	return nil
}

func (d DataFilter) MarshalText() ([]byte, error) {
	return []byte(d.String()), nil
}

// ParseDataFilterは、DataFilterの文字列表現からDataFilterを生成します。
func ParseDataFilter(str string) (*DataFilter, error) {
	var res DataFilter
	if err := res.UnmarshalText([]byte(str)); err != nil {
		return nil, err
	}
	return &res, nil
}

// MustParseDataFilterは、DataFilterの文字列表現からDataFilterを生成します。
//
// パースに失敗した場合はpanicします。
func MustParseDataFilter(str string) *DataFilter {
	res, err := ParseDataFilter(str)
	if err != nil {
		panic(err)
	}
	return res
}
