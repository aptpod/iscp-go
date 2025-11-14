package transport

import (
	"encoding/json"
	"fmt"

	"github.com/aptpod/iscp-go/errors"
	"github.com/aptpod/iscp-go/transport/compress"
)

// EncodingName は、エンコーディングの識別名を表します。
type EncodingName string

const (
	// EncodingNameJSON は、 JSON 形式のエンコーディングを表す名称です。
	EncodingNameJSON EncodingName = "json"

	// EncodingNameProtobuf は、 Protocol Buffers 形式のエンコーディングを表す名称です。
	EncodingNameProtobuf EncodingName = "proto"

	DefaultCompressionLevel = 6
)

type NegotiationParams struct {
	Encoding           EncodingName  `json:"enc,omitempty"`
	Compress           compress.Type `json:"comp,omitempty"`
	CompressLevel      *int          `json:"clevel,string,omitempty"`
	CompressWindowBits *int          `json:"cwinbits,string,omitempty"`

	TransportID TransportID `json:"tid,omitempty"`

	TransportGroupID TransportGroupID `json:"tgid,omitempty"`

	// Reconnection layer parameters
	PingInterval *int `json:"pinterval,string,omitempty"` // Ping interval in milliseconds
	ReadTimeout  *int `json:"rtimeout,string,omitempty"`  // Read timeout in milliseconds
}

func (p *NegotiationParams) Validate() error {
	switch p.Encoding {
	case "", EncodingNameJSON, EncodingNameProtobuf: // ok
	default:
		return errors.Errorf("unknown encoding type %q", p.Encoding)
	}

	switch p.Compress {
	case "":
		// ok
	case compress.TypePerMessage, compress.TypeContextTakeOver:
		if p.CompressLevel != nil {
			if *p.CompressLevel < 0 || *p.CompressLevel > 9 {
				return errors.Errorf("unknown compress level %d", p.CompressLevel)
			}
		} else {
			compLevel := DefaultCompressionLevel
			p.CompressLevel = &compLevel
		}
		if p.CompressWindowBits != nil {
			if *p.CompressWindowBits < 0 || *p.CompressWindowBits > 32 {
				return errors.Errorf("invalid compress window bits %d", p.CompressWindowBits)
			}
		}
	default:
		return errors.Errorf("unknown compress type %q", p.Compress)
	}

	// Validate reconnection layer parameters
	if p.PingInterval != nil && *p.PingInterval <= 0 {
		return errors.Errorf("ping interval must be positive, got %d", *p.PingInterval)
	}
	if p.ReadTimeout != nil && *p.ReadTimeout <= 0 {
		return errors.Errorf("read timeout must be positive, got %d", *p.ReadTimeout)
	}

	return nil
}

// CompressConfig は、事前ネゴシエーションの情報をもとに設定された新たな compress.Config を返します。
func (p *NegotiationParams) CompressConfig(base compress.Config) compress.Config {
	if p.CompressLevel == nil || *p.CompressLevel == 0 {
		base.Enable = false
		return base
	}
	base.Enable = true
	if p.CompressLevel != nil {
		base.Level = *p.CompressLevel
	}
	if p.CompressWindowBits != nil {
		base.WindowBits = *p.CompressWindowBits
	}

	switch p.Compress {
	case compress.TypePerMessage:
		base.DisableContextTakeover = true
	case compress.TypeContextTakeOver:
		base.DisableContextTakeover = false
	}

	return base
}

func (p *NegotiationParams) UnmarshalKeyValues(keyvals map[string]string) error {
	// 文字列のbool値を適切に変換するための中間マップ
	converted := make(map[string]interface{})
	for k, v := range keyvals {
		converted[k] = v
	}

	b, err := json.Marshal(converted)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(b, p); err != nil {
		return err
	}
	return nil
}

func (p *NegotiationParams) MarshalKeyValues() (map[string]string, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}

	keyvals := make(map[string]any)
	if err := json.Unmarshal(b, &keyvals); err != nil {
		return nil, err
	}
	res := make(map[string]string, len(keyvals))
	for k, v := range keyvals {
		res[k] = fmt.Sprintf("%v", v)
	}
	return res, nil
}
