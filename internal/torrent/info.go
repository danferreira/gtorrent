package torrent

import (
	"github.com/danferreira/gtorrent/internal/metadata"
	"github.com/danferreira/gtorrent/internal/state"
)

type TorrentInfo struct {
	Metadata *metadata.Metadata
	State    *state.State
}

func NewStats(m *metadata.Metadata, state *state.State) *TorrentInfo {
	return &TorrentInfo{m, state}
}
