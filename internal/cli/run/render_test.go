package run

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLastJSONLine_FindsLastObject(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "single JSON line",
			input:    `{"foo":"bar"}`,
			expected: `{"foo":"bar"}`,
		},
		{
			name:     "multiple lines returns last JSON object",
			input:    "debug output\n{\"first\":1}\nmore output\n{\"last\":2}",
			expected: `{"last":2}`,
		},
		{
			name:     "trailing newline handled",
			input:    "{\"x\":1}\n",
			expected: `{"x":1}`,
		},
		{
			name:     "no JSON returns nil",
			input:    "plain text output\nno json here",
			expected: "",
		},
		{
			name:     "empty input returns nil",
			input:    "",
			expected: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := lastJSONLine([]byte(tc.input))
			if tc.expected == "" {
				assert.Nil(t, result)
			} else {
				assert.Equal(t, tc.expected, string(result))
			}
		})
	}
}

func TestPushRenderEvent_NilServerNoOp(t *testing.T) {
	// When no terminal server is running, pushRenderEvent must not panic.
	buf := bytes.NewBufferString(`{"render":{"type":"table","title":"T","payload":{}}}`)
	assert.NotPanics(t, func() {
		pushRenderEvent(buf, "test-skill")
	})
}

func TestPushRenderEvent_NoRenderFieldIsNoop(t *testing.T) {
	// stdout without a render field must not attempt a push.
	buf := bytes.NewBufferString(`{"summary":"ok","status":"healthy","data":{}}`)
	assert.NotPanics(t, func() {
		pushRenderEvent(buf, "test-skill")
	})
}

func TestPushRenderEvent_EmptyBufferIsNoop(t *testing.T) {
	buf := &bytes.Buffer{}
	assert.NotPanics(t, func() {
		pushRenderEvent(buf, "test-skill")
	})
}
