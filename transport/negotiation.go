package transport

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"unicode/utf8"

	"github.com/aptpod/iscp-go/v2/errors"
	"github.com/aptpod/iscp-go/v2/transport/compress"
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

	SuperConnectionID SuperConnectionID `json:"superid,omitempty"`

	SubConnectionID SubConnectionID `json:"subid,omitempty"`

	// トランスポート種別（ws2, quic2, webtrans2）
	TransportType Name `json:"trans,omitempty"`

	// 再接続最大試行回数
	MaxReconnectAttempts *int `json:"rretry,string,omitempty"`

	// 再接続間隔（秒）
	ReconnectInterval *int `json:"rinterval,string,omitempty"`

	// ハートビート間隔（秒）
	HeartbeatInterval *int `json:"hbinterval,string,omitempty"`
	// ハートビートタイムアウト（秒）
	HeartbeatTimeout *int `json:"hbtimeout,string,omitempty"`
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

	// ハートビートパラメータのバリデーション
	if p.HeartbeatInterval != nil {
		if *p.HeartbeatInterval <= 0 {
			return errors.Errorf("heartbeat interval must be positive, got %d", *p.HeartbeatInterval)
		}
		if *p.HeartbeatInterval > 65535 {
			return errors.Errorf("heartbeat interval must be at most 65535, got %d", *p.HeartbeatInterval)
		}
	}
	if p.HeartbeatTimeout != nil {
		if *p.HeartbeatTimeout <= 0 {
			return errors.Errorf("heartbeat timeout must be positive, got %d", *p.HeartbeatTimeout)
		}
		if *p.HeartbeatTimeout > 65535 {
			return errors.Errorf("heartbeat timeout must be at most 65535, got %d", *p.HeartbeatTimeout)
		}
	}
	if p.HeartbeatInterval != nil && p.HeartbeatTimeout != nil {
		if *p.HeartbeatInterval >= *p.HeartbeatTimeout {
			return errors.Errorf("heartbeat interval (%d) must be less than heartbeat timeout (%d)", *p.HeartbeatInterval, *p.HeartbeatTimeout)
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
	base.Level = *p.CompressLevel
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

// MarshalURLValues は、ネゴシエーションパラメータを url.Values にエンコードします。
// WebSocket および WebTransport で使用します。
func (p *NegotiationParams) MarshalURLValues() (url.Values, error) {
	keyvals, err := p.MarshalKeyValues()
	if err != nil {
		return nil, err
	}
	res := url.Values{}
	for k, v := range keyvals {
		res[k] = []string{v}
	}
	return res, nil
}

// UnmarshalURLValues は、ネゴシエーションパラメータを url.Values からデコードします。
// WebSocket および WebTransport で使用します。
func (p *NegotiationParams) UnmarshalURLValues(values url.Values) error {
	keyvals := map[string]string{}
	for k, v := range values {
		if len(k) == 0 {
			return errors.New("got empty keyname")
		}
		if len(v) != 1 {
			return errors.Errorf("value's len must be one, got %d values in %q", len(v), k)
		}
		keyvals[k] = v[0]
	}
	return p.UnmarshalKeyValues(keyvals)
}

// MarshalBinaryKeyValues は、ネゴシエーションパラメータを QUIC 用にバイナリエンコードします。
func (p *NegotiationParams) MarshalBinaryKeyValues() ([]byte, error) {
	keyvals, err := p.MarshalKeyValues()
	if err != nil {
		return nil, err
	}
	var res []byte
	lenbuf := make([]byte, 2)
	for k, v := range keyvals {
		binary.BigEndian.PutUint16(lenbuf, uint16(len(k)))
		res = append(res, lenbuf...)
		res = append(res, []byte(k)...)
		binary.BigEndian.PutUint16(lenbuf, uint16(len(v)))
		res = append(res, lenbuf...)
		res = append(res, []byte(v)...)
	}
	return res, nil
}

// UnmarshalBinaryKeyValues は、ネゴシエーションパラメータを QUIC 用にバイナリデコードします。
func (p *NegotiationParams) UnmarshalBinaryKeyValues(b []byte) error {
	keyvals, err := readBinaryKeyValues(bytes.NewReader(b))
	if err != nil {
		return err
	}
	return p.UnmarshalKeyValues(keyvals)
}

func readBinaryKeyValues(r io.Reader) (map[string]string, error) {
	keyvals := map[string]string{}
	lenBuf := make([]byte, 2)
	for {
		if _, err := io.ReadFull(r, lenBuf); err != nil {
			if err == io.EOF {
				break
			}
			return nil, errors.Errorf("error while reading len: %w", err)
		}
		keyLen := binary.BigEndian.Uint16(lenBuf)
		if keyLen == 0 {
			return nil, errors.New("got empty keyname")
		}
		keyBuf := make([]byte, keyLen)
		if _, err := io.ReadFull(r, keyBuf); err != nil {
			return nil, errors.Errorf("error while reading %d bytes key: %w", keyLen, err)
		}
		if !utf8.Valid(keyBuf) {
			return nil, errors.New("key must be UTF-8 encoded")
		}
		if _, err := io.ReadFull(r, lenBuf); err != nil {
			return nil, errors.Errorf("error while reading len: %w", err)
		}
		valLen := binary.BigEndian.Uint16(lenBuf)
		valBuf := make([]byte, valLen)
		if _, err := io.ReadFull(r, valBuf); err != nil {
			return nil, errors.Errorf("error while reading %d bytes value: %w", valLen, err)
		}
		if !utf8.Valid(valBuf) {
			return nil, errors.New("value must be UTF-8 encoded")
		}
		key := string(keyBuf)
		val := string(valBuf)
		if _, ok := keyvals[key]; ok {
			return nil, errors.Errorf("duplicated key: %s", key)
		}
		keyvals[key] = val
	}
	return keyvals, nil
}
