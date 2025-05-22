package torrent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"log/slog"
	"net"
	"sync"

	"github.com/danferreira/gtorrent/internal/handshake"
	"github.com/danferreira/gtorrent/internal/metadata"
	"github.com/danferreira/gtorrent/internal/piece"
)

type Client struct {
	mu sync.RWMutex

	PeerID [20]byte

	torrents map[[20]byte]*Torrent

	statsIn  chan piece.TorrentStats
	statsOut chan piece.TorrentStats

	ctx    context.Context
	cancel context.CancelFunc
}

func NewClient() *Client {
	in := make(chan piece.TorrentStats, 64)
	out := make(chan piece.TorrentStats, 64)
	ctx, cancel := context.WithCancel(context.Background())

	e := &Client{
		torrents: make(map[[20]byte]*Torrent),
		PeerID:   generatePeerID(),
		statsIn:  in,
		statsOut: out,
		ctx:      ctx,
		cancel:   cancel,
	}

	return e
}

func (c *Client) AddFile(path string) {
	m, err := metadata.Parse(path)
	if err != nil {
		slog.Error("Cannot parse file", "path", path)
		log.Fatal(err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	hash := m.Info.InfoHash

	if _, ok := c.torrents[hash]; ok {
		return
	}

	t, err := NewTorrent(m, c.PeerID)
	if err != nil {
		slog.Error("Cannot add torrent", "path", path)
		log.Fatal(err)
	}

	go t.Start(c.ctx)
	go c.statsFan()

	c.torrents[hash] = t
}

func (c *Client) ListenForPeers() error {
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
			go c.connectToPeer(conn)
		}
	}(ln)

	return nil
}

func (c *Client) statsFan() {
	for {
		select {
		case snap := <-c.statsIn:
			select {
			case c.statsOut <- snap:
			default:
			}

		case <-c.ctx.Done(): // <- graceful shutdown
			return
		}
	}
}

func (c *Client) Stats() <-chan piece.TorrentStats { return c.statsOut }

func (c *Client) connectToPeer(conn net.Conn) error {
	h, err := handshake.Read(conn)
	if err != nil {
		return err
	}

	infoHash := h.InfoHash
	t, ok := c.torrents[infoHash]
	if !ok {
		slog.Warn("Cannot find any torrent with this hash", "hash", infoHash)
		return nil
	}

	hs := handshake.Handshake{
		InfoHash: infoHash,
		PeerID:   c.PeerID,
	}

	err = hs.Write(conn)
	if err != nil {
		return err
	}

	t.NewPeerConn(conn)

	return nil
}

func generatePeerID() [20]byte {
	const prefix = "-GT0001-"
	var id [20]byte
	copy(id[:], prefix)

	// 12 random bytes -> 24 hex chars, but we need 12 *ASCII* bytes.
	var tail [12]byte
	if _, err := rand.Read(tail[:]); err != nil {
		log.Fatal(err) // rand failure is unrecoverable here
	}

	// Encode to printable ASCII using hex (2 chars per byte) and take first 12.
	hex.Encode(id[8:], tail[:6]) // 6 bytes * 2 hex chars = 12 ASCII bytes

	return id
}
