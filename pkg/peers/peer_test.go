package peers

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUnmarshal(t *testing.T) {
	tests := []struct {
		name    string
		input   []byte
		output  Peer
		wantErr string
	}{
		{"correctly parses peer address", []byte{127, 0, 0, 1, 0x1A, 0xE1}, Peer{
			SocketAddress: "127.0.0.1:6881",
		}, "",
		},
		{"fails with invalid address", []byte{127, 0, 0, 1}, Peer{}, "invalid peer address"},
	}

	for _, tt := range tests {
		peer, err := Unmarshal(tt.input)

		if tt.wantErr != "" {
			assert.Contains(t, err.Error(), tt.wantErr)
		} else {
			assert.Nil(t, err)
		}

		assert.Equal(t, tt.output, peer)
	}
}
