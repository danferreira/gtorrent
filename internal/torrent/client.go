package torrent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"log/slog"
	"net"
	"sync"

	"github.com/danferreira/gtorrent/internal/handshake"
	"github.com/danferreira/gtorrent/internal/metadata"
	"github.com/danferreira/gtorrent/internal/piece"
)

type TorrentFactory interface {
	NewTorrent(m *metadata.Metadata, peerID [20]byte, listenPort int) (TorrentRunner, error)
}

type TorrentRunner interface {
	Start(ctx context.Context) error
}

type DefaultTorrentFactory struct{}

func (d DefaultTorrentFactory) NewTorrent(m *metadata.Metadata, peerID [20]byte, listenPort int) (TorrentRunner, error) {
	return NewTorrent(m, peerID, listenPort)
}

type Client struct {
	mu sync.RWMutex

	peerID [20]byte

	torrentFactory TorrentFactory
	torrents       map[[20]byte]TorrentRunner

	statsIn  chan piece.TorrentStats
	statsOut chan piece.TorrentStats

	ctx    context.Context
	cancel context.CancelFunc

	listenPort int
}

var (
	NewTorrentFactory = NewTorrent
)

func NewClient(listenPort int) *Client {
	return NewClientWithDeps(DefaultTorrentFactory{}, listenPort)
}

func NewClientWithDeps(factory TorrentFactory, listenPort int) *Client {
	in := make(chan piece.TorrentStats, 64)
	out := make(chan piece.TorrentStats, 64)
	ctx, cancel := context.WithCancel(context.Background())

	e := &Client{
		torrentFactory: factory,
		torrents:       make(map[[20]byte]TorrentRunner),
		peerID:         generatePeerID(),
		statsIn:        in,
		statsOut:       out,
		ctx:            ctx,
		cancel:         cancel,
		listenPort:     listenPort,
	}

	return e
}

func (c *Client) AddFile(path string) error {
	m, err := metadata.Parse(path)
	if err != nil {
		return fmt.Errorf("failed to parse torrent file %s: %w", path, err)
	}

	infoHash := m.Info.InfoHash
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.torrents[infoHash]; ok {
		return fmt.Errorf("torrent already added: %s", path)
	}

	t, err := c.torrentFactory.NewTorrent(m, c.peerID, c.listenPort)
	if err != nil {
		return fmt.Errorf("failed to create torrent: %w", err)
	}

	c.torrents[infoHash] = t

	return nil
}

func (c *Client) StartTorrent(infoHash [20]byte) error {
	c.mu.RLock()
	entry, exists := c.torrents[infoHash]
	c.mu.RUnlock()

	if !exists {
		return fmt.Errorf("torrent not found")
	}

	go func() {
		defer func() {
			c.mu.Lock()
			delete(c.torrents, infoHash)
			c.mu.Unlock()
		}()

		if err := entry.Start(c.ctx); err != nil {
			slog.Error("torrent stopped with error", "infoHash", infoHash, "err", err)
		} else {
			slog.Info("torrent finished")
		}
	}()

	return nil
}

func (c *Client) ListenForInboundPeers() error {
	slog.Info("Listening for incoming peers", "port", c.listenPort)

	ln, err := net.Listen("tcp4", fmt.Sprintf(":%d", c.listenPort))
	if err != nil {
		return fmt.Errorf("failed to start listener: %w", err)
	}

	go func() {
		defer ln.Close()

		for {
			select {
			case <-c.ctx.Done():
				return
			default:
				conn, err := ln.Accept()
				if err != nil {
					slog.Error("Error during accepting new conn", "error", err)
					continue
				}
				go func() {
					err := c.handleInboundConnection(conn)
					if err != nil {
						slog.Debug("inbound connection failed", "error", err)

						conn.Close()
					}
				}()
			}
		}
	}()

	return nil
}

func (c *Client) Close() {
	c.cancel()
}
func (c *Client) Stats() <-chan piece.TorrentStats { return c.statsOut }

func (c *Client) handleInboundConnection(conn net.Conn) error {
	h, err := handshake.Read(conn)
	if err != nil {
		return err
	}

	infoHash := h.InfoHash
	c.mu.RLock()
	entry, ok := c.torrents[infoHash]
	c.mu.RUnlock()

	if !ok {
		return fmt.Errorf("Cannot find any torrent with this hash: %s", infoHash)
	}

	if torrent, ok := entry.(*Torrent); ok {
		torrent.NewInboundConnection(conn)
	}

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
