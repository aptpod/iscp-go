package quic

import (
	"bytes"
	"encoding/binary"
	"io"
	"unicode/utf8"

	"github.com/aptpod/iscp-go/errors"
	"github.com/aptpod/iscp-go/transport"
)

// NegotiationParamsは、トランスポートネゴシエーションのパラメータです。
type NegotiationParams struct {
	transport.NegotiationParams
}

// Marshalは、ネゴシエーションパラメータをQUIC用にバイナリエンコードします。
func (p *NegotiationParams) Marshal() ([]byte, error) {
	keyvals, err := p.MarshalKeyValues()
	if err != nil {
		return nil, err
	}

	res := make([]byte, 0)
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

// Unmarshalは、ネゴシエーションパラメータをQUIC用にバイナリデコードします。
func (p *NegotiationParams) Unmarshal(b []byte) error {
	keyvals, err := readKeyValues(bytes.NewReader(b))
	if err != nil {
		return err
	}

	return p.UnmarshalKeyValues(keyvals)
}

func readKeyValues(r io.Reader) (map[string]string, error) {
	keyvals := map[string]string{}
	lenBuf := make([]byte, 2)

	for {
		if _, err := io.ReadFull(r, lenBuf); err != nil {
			if err == io.EOF { // no more key-value
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
