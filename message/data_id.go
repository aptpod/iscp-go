package message

// DataIDは、データポイントの、名称とデータ型を表す識別子です。
//
// おもに、ブローカーおよびノードでのデータの意味と型の特定、
// ダウンストリームフィルタにて指定された受信条件に各時系列データポイントが合致するかどうかの判定、などに使用されます。
//
// 特殊文字 `/` はセパレータです。名称/型の階層構造を表現することができます。
type DataID struct {
	Name string // 名称
	Type string // 型
}

func (d DataID) String() string {
	return (&DataFilter{Name: d.Name, Type: d.Type}).String()
}

func (d *DataID) UnmarshalText(src []byte) error {
	var f DataFilter
	if err := f.UnmarshalText(src); err != nil {
		return err
	}
	*d = *newDataIDFromDataFilter(&f)
	return nil
}

func (d DataID) MarshalText() ([]byte, error) {
	return []byte(d.String()), nil
}

// ParseDataIDは、DataIDの文字列表現からDataIDを生成します。
func ParseDataID(str string) (*DataID, error) {
	res, err := ParseDataFilter(str)
	if err != nil {
		return nil, err
	}
	return newDataIDFromDataFilter(res), nil
}

// MustParseDataIDは、DataIDの文字列表現からDataIDを生成します。
//
// パースに失敗した場合はpanicします。
func MustParseDataID(str string) *DataID {
	return newDataIDFromDataFilter(MustParseDataFilter(str))
}

func newDataIDFromDataFilter(d *DataFilter) *DataID {
	return &DataID{
		Name: d.Name,
		Type: d.Type,
	}
}
