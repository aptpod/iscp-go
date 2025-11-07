package encoding_test

import (
	"fmt"
	"os"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/aptpod/iscp-go/message"
)

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

func BenchmarkKeyType(b *testing.B) {
	type keyFunc func(req message.Message) interface{}
	testCases := []struct {
		name    string
		keyFunc keyFunc
	}{
		{
			name:    "fmtSprintf",
			keyFunc: func(req message.Message) interface{} { return fmt.Sprintf("%T", req) },
		},
		{
			name:    "typeOf",
			keyFunc: func(req message.Message) interface{} { return reflect.TypeOf(req).String() },
		},
	}

	for _, tt := range testCases {
		var req message.UpstreamOpenRequest
		b.Run(tt.name, func(b *testing.B) {
			a := map[interface{}]int{}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				a[tt.keyFunc(&req)] = i
			}
		})
	}
}

func TestKeyType(t *testing.T) {
	var req message.UpstreamOpenRequest
	var req2 message.UpstreamOpenRequest
	var req3 message.DownstreamCall
	assert.Equal(t, reflect.TypeOf(req), reflect.TypeOf(req2))
	assert.Equal(t, reflect.TypeOf(&req), reflect.TypeOf(&req2))
	assert.NotEqual(t, reflect.TypeOf(&req), reflect.TypeOf(req2))
	assert.NotEqual(t, reflect.TypeOf(req), reflect.TypeOf(req3))

	m := map[reflect.Type]int{}
	m[reflect.TypeOf(req)] = 1
	_, ok := m[reflect.TypeOf(req)]
	assert.True(t, ok)
	_, ok = m[reflect.TypeOf(req2)]
	assert.True(t, ok)
	_, ok = m[reflect.TypeOf(&req2)]
	assert.False(t, ok)
	_, ok = m[reflect.TypeOf(req3)]
	assert.False(t, ok)
}
