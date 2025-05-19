package engine

import (
	"context"
	"log"
	"sync"

	"github.com/danferreira/gtorrent/internal/metadata"
	"github.com/danferreira/gtorrent/internal/piece"
	"github.com/danferreira/gtorrent/internal/torrent"
)

type Engine struct {
	mu sync.RWMutex

	torrents map[[20]byte]*torrent.Torrent

	statsIn  chan piece.TorrentStats
	statsOut chan piece.TorrentStats

	ctx    context.Context
	cancel context.CancelFunc
}

func NewEngine() *Engine {
	in := make(chan piece.TorrentStats, 64)
	out := make(chan piece.TorrentStats, 64)

	ctx, cancel := context.WithCancel(context.Background())

	e := &Engine{
		torrents: make(map[[20]byte]*torrent.Torrent),
		statsIn:  in,
		statsOut: out,
		ctx:      ctx,
		cancel:   cancel,
	}
	go e.statsFan() // start aggregator
	return e
}

func (e *Engine) statsFan() {
	for {
		select {
		case snap := <-e.statsIn:
			select {
			case e.statsOut <- snap:
			default:
			}

		case <-e.ctx.Done(): // <- graceful shutdown
			return
		}
	}
}
func (e *Engine) Stats() <-chan piece.TorrentStats { return e.statsOut }

func (e *Engine) AddFile(path string) {
	m, err := metadata.Parse(path)
	if err != nil {
		log.Fatal(err)
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if _, ok := e.torrents[m.Info.InfoHash]; ok {
		return
	}
	var peerID [20]byte
	copy(peerID[:], "-GT0001-abcdefgh1234")

	t := torrent.Torrent{
		Metadata:  *m,
		PeerID:    peerID,
		StatsChan: e.statsIn,
	}

	go t.Run(e.ctx)

	e.torrents[t.Metadata.Info.InfoHash] = &t
}
