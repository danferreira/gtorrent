package message

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRead(t *testing.T) {
	tests := map[string]struct {
		input      []byte
		output     *Message
		shouldFail bool
	}{
		"pass message":                {[]byte{0, 0, 0, 5, 5, 1, 2, 3, 4}, &Message{ID: 5, Payload: []byte{1, 2, 3, 4}}, false},
		"keep alive message":          {[]byte{0, 0, 0, 0}, nil, false},
		"mal constructed buffer":      {[]byte{0}, nil, true},
		"buffer too short for length": {[]byte{0, 0, 0, 5, 4, 1, 2}, nil, true},
	}

	for _, tt := range tests {

		buffer := bytes.NewBuffer(tt.input)
		m, err := Read(buffer)
		if tt.shouldFail {
			assert.NotNil(t, err)
		} else {
			assert.Nil(t, err)
			assert.Equal(t, tt.output, m)
		}
	}
}
