package transport

import (
	"compress/zlib"
	"encoding/json"

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
)

type NegotiationParams struct {
	Encoding           EncodingName  `json:"enc,omitempty"`
	Compress           compress.Type `json:"comp,omitempty"`
	CompressLevel      *int          `json:"clevel,string,omitempty"`
	CompressWindowBits *int          `json:"cwinbits,string,omitempty"`
}

func (p NegotiationParams) Validate() error {
	switch p.Encoding {
	case "", EncodingNameJSON, EncodingNameProtobuf: // ok
	default:
		return errors.Errorf("unknown encoding type %q", p.Encoding)
	}

	switch p.Compress {
	case "", compress.TypePerMessage, compress.TypeContextTakeOver: // ok
	default:
		return errors.Errorf("unknown compress type %q", p.Compress)
	}

	if p.CompressLevel != nil {
		switch *p.CompressLevel {
		case zlib.NoCompression, zlib.BestSpeed, zlib.BestCompression, zlib.DefaultCompression, zlib.HuffmanOnly:
			// ok
		default:
			return errors.Errorf("unknown compress level %d", p.CompressLevel)
		}
	}

	if p.CompressWindowBits != nil {
		if *p.CompressWindowBits < 0 || *p.CompressWindowBits > 32 {
			return errors.Errorf("invalid compress window bits %d", p.CompressWindowBits)
		}
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
	b, err := json.Marshal(keyvals)
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

	keyvals := make(map[string]string)
	if err := json.Unmarshal(b, &keyvals); err != nil {
		return nil, err
	}
	return keyvals, nil
}
