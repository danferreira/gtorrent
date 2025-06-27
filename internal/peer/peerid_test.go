package peer

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewPeerID(t *testing.T) {
	peerID := NewPeerID()

	assert.Equal(t, 20, len(peerID))
	assert.True(t, strings.HasPrefix(string(peerID[:]), "-GT0001-"))
}
