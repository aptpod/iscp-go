package webtransport

import (
	"net/url"

	"github.com/aptpod/iscp-go/errors"

	"github.com/aptpod/iscp-go/transport"
)

// NegotiationParamsは、トランスポートネゴシエーションのパラメータです。
type NegotiationParams struct {
	transport.NegotiationParams
}

// MarshalURLValuesは、ネゴシエーションパラメータをurl.Valuesにエンコードします。
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

// UnmarshalURLValuesは、ネゴシエーションパラメータをurl.Valuesからデコードします。
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
