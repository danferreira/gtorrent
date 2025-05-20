package engine

import (
	"context"
	"log"
	"log/slog"
	"net"
	"sync"

	"github.com/danferreira/gtorrent/internal/handshake"
	"github.com/danferreira/gtorrent/internal/metadata"
	"github.com/danferreira/gtorrent/internal/piece"
	"github.com/danferreira/gtorrent/internal/torrent"
)

type Engine struct {
	mu sync.RWMutex

	peerID   [20]byte
	torrents map[[20]byte]*torrent.Torrent

	statsIn  chan piece.TorrentStats
	statsOut chan piece.TorrentStats

	ctx    context.Context
	cancel context.CancelFunc
}

func NewEngine() *Engine {
	in := make(chan piece.TorrentStats, 64)
	out := make(chan piece.TorrentStats, 64)

	var peerID [20]byte
	copy(peerID[:], "-GT0001-abcdefgh1234")

	ctx, cancel := context.WithCancel(context.Background())

	e := &Engine{
		torrents: make(map[[20]byte]*torrent.Torrent),
		peerID:   peerID,
		statsIn:  in,
		statsOut: out,
		ctx:      ctx,
		cancel:   cancel,
	}
	go e.statsFan()
	go e.ListenForPeers()
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

	t := torrent.Torrent{
		Metadata:  *m,
		PeerID:    e.peerID,
		StatsChan: e.statsIn,
	}

	go t.Run(e.ctx)

	e.torrents[t.Metadata.Info.InfoHash] = &t
}

func (e *Engine) ListenForPeers() error {
	slog.Info("Listen for connections")
	ln, err := net.Listen("tcp4", ":6881")
	if err != nil {
		return err
	}

	go func(ln net.Listener) {
		for {
			conn, err := ln.Accept()
			if err != nil {
				slog.Error("Error during accepting new conn", "error", err)
				continue
			}
			go e.connectToPeer(conn)
		}
	}(ln)

	return nil
}

func (e *Engine) connectToPeer(conn net.Conn) error {
	h, err := handshake.Read(conn)
	if err != nil {
		return err
	}

	infoHash := h.InfoHash
	t, ok := e.torrents[infoHash]
	if !ok {
		slog.Warn("Cannot find any torrent with this hash", "hash", infoHash)
		return nil
	}

	hs := handshake.Handshake{
		InfoHash: infoHash,
		PeerID:   e.peerID,
	}

	err = hs.Write(conn)
	if err != nil {
		return err
	}

	t.NewPeerConn(conn)

	return nil
}
