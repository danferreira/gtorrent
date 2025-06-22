package torrent

import (
	"testing"

	"github.com/danferreira/gtorrent/internal/metadata"
	"github.com/stretchr/testify/assert"
)

func TestNewTorrent(t *testing.T) {
	m := &metadata.Metadata{}
	pid := [20]byte{}
	tr, err := NewTorrent(m, pid, 6881)

	assert.NoError(t, err)
	assert.NotNil(t, tr)
	assert.Equal(t, m, tr.metadata)
	assert.Equal(t, pid, tr.PeerID)
	assert.NotNil(t, tr.inboundConnections)
	assert.NotNil(t, tr.trackerManager)
	assert.NotNil(t, tr.pieceScheduler)
	assert.NotNil(t, tr.peerManager)
	assert.NotNil(t, tr.State)
}
