package multi_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/aptpod/iscp-go/transport/multi"
)

func TestECFConstants(t *testing.T) {
	tests := []struct {
		name     string
		constant int
		expected int
	}{
		{
			name:     "success: ecfBeta is 4",
			constant: multi.EcfBeta,
			expected: 4,
		},
		{
			name:     "success: mss is 1460",
			constant: multi.MSS,
			expected: 1460,
		},
		{
			name:     "success: defaultCWND is 14600 (10 * mss)",
			constant: multi.DefaultCWND,
			expected: 14600,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.constant)
		})
	}
}

func TestAbs(t *testing.T) {
	tests := []struct {
		name     string
		input    time.Duration
		expected time.Duration
	}{
		{
			name:     "success: positive value",
			input:    100 * time.Millisecond,
			expected: 100 * time.Millisecond,
		},
		{
			name:     "success: negative value returns positive",
			input:    -100 * time.Millisecond,
			expected: 100 * time.Millisecond,
		},
		{
			name:     "edge case: zero",
			input:    0,
			expected: 0,
		},
		{
			name:     "success: large positive value",
			input:    10 * time.Second,
			expected: 10 * time.Second,
		},
		{
			name:     "success: large negative value returns positive",
			input:    -10 * time.Second,
			expected: 10 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := multi.Abs(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRTTToMicroseconds(t *testing.T) {
	tests := []struct {
		name     string
		input    time.Duration
		expected uint64
	}{
		{
			name:     "success: 1 millisecond",
			input:    1 * time.Millisecond,
			expected: 1000,
		},
		{
			name:     "success: 1 second",
			input:    1 * time.Second,
			expected: 1000000,
		},
		{
			name:     "success: 100 microseconds",
			input:    100 * time.Microsecond,
			expected: 100,
		},
		{
			name:     "edge case: zero",
			input:    0,
			expected: 0,
		},
		{
			name:     "success: 50 milliseconds",
			input:    50 * time.Millisecond,
			expected: 50000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := multi.RTTToMicroseconds(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMicrosecondsToRTT(t *testing.T) {
	tests := []struct {
		name     string
		input    uint64
		expected time.Duration
	}{
		{
			name:     "success: 1000 microseconds to 1ms",
			input:    1000,
			expected: 1 * time.Millisecond,
		},
		{
			name:     "success: 1000000 microseconds to 1s",
			input:    1000000,
			expected: 1 * time.Second,
		},
		{
			name:     "success: 100 microseconds",
			input:    100,
			expected: 100 * time.Microsecond,
		},
		{
			name:     "edge case: zero",
			input:    0,
			expected: 0,
		},
		{
			name:     "success: 50000 microseconds to 50ms",
			input:    50000,
			expected: 50 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := multi.MicrosecondsToRTT(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRTTConversionRoundTrip(t *testing.T) {
	tests := []struct {
		name     string
		original time.Duration
	}{
		{
			name:     "success: 1 millisecond round trip",
			original: 1 * time.Millisecond,
		},
		{
			name:     "success: 100 milliseconds round trip",
			original: 100 * time.Millisecond,
		},
		{
			name:     "success: 1 second round trip",
			original: 1 * time.Second,
		},
		{
			name:     "success: 50 microseconds round trip",
			original: 50 * time.Microsecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// time.Duration -> uint64 -> time.Duration の往復変換
			microseconds := multi.RTTToMicroseconds(tt.original)
			converted := multi.MicrosecondsToRTT(microseconds)
			assert.Equal(t, tt.original, converted, "往復変換で値が保持されるべき")
		})
	}
}
