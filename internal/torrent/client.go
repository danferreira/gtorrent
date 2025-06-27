package torrent

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"

	"github.com/danferreira/gtorrent/internal/handshake"
	"github.com/danferreira/gtorrent/internal/metadata"
	"github.com/danferreira/gtorrent/internal/peer"
	"github.com/danferreira/gtorrent/internal/state"
)

type TorrentFactory interface {
	NewTorrent(m *metadata.Metadata, peerID peer.PeerID, listenPort int) (TorrentRunner, error)
}

type TorrentRunner interface {
	Start(ctx context.Context)
	Stop()
	State() *state.State
}

type DefaultTorrentFactory struct{}

func (d DefaultTorrentFactory) NewTorrent(m *metadata.Metadata, peerID peer.PeerID, listenPort int) (TorrentRunner, error) {
	return NewTorrent(m, peerID, listenPort)
}

type Config struct {
	MaxActiveTorrents int
	ListenPort        int
}

type Client struct {
	mu sync.RWMutex

	config Config

	peerID peer.PeerID

	torrentFactory TorrentFactory
	torrents       map[[20]byte]TorrentRunner

	ctx    context.Context
	cancel context.CancelFunc

	activeTorrents chan struct{}
}

func NewClient(config Config) *Client {
	return NewClientWithDeps(DefaultTorrentFactory{}, config)
}

func NewClientWithDeps(factory TorrentFactory, config Config) *Client {
	ctx, cancel := context.WithCancel(context.Background())

	e := &Client{
		config:         config,
		torrentFactory: factory,
		torrents:       make(map[[20]byte]TorrentRunner),
		peerID:         peer.NewPeerID(),
		ctx:            ctx,
		cancel:         cancel,

		activeTorrents: make(chan struct{}, config.MaxActiveTorrents),
	}

	return e
}

func (c *Client) AddFile(path string) (*TorrentInfo, error) {
	slog.Info("adding new file", "path", path)
	m, err := metadata.Parse(path)
	if err != nil {
		return nil, fmt.Errorf("failed to parse torrent file %s: %w", path, err)
	}

	infoHash := m.Info.InfoHash
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.torrents[infoHash]; ok {
		return nil, fmt.Errorf("torrent already added: %s", path)
	}

	t, err := c.torrentFactory.NewTorrent(m, c.peerID, c.config.ListenPort)
	if err != nil {
		return nil, fmt.Errorf("failed to create torrent: %w", err)
	}

	c.torrents[infoHash] = t

	ti := &TorrentInfo{
		Metadata: m,
		State:    t.State(),
	}

	return ti, nil
}

func (c *Client) StartTorrent(infoHash [20]byte) error {
	slog.Info("starting torrent")
	c.mu.RLock()
	entry, exists := c.torrents[infoHash]
	c.mu.RUnlock()

	if !exists {
		return fmt.Errorf("torrent not found")
	}

	go func() {
		select {
		case c.activeTorrents <- struct{}{}:
			entry.Start(c.ctx)
			<-c.activeTorrents
		case <-c.ctx.Done():
			return
		}

	}()

	return nil
}

func (c *Client) StopTorrent(infoHash [20]byte) error {
	slog.Info("stopping torrent")
	c.mu.RLock()
	entry, exists := c.torrents[infoHash]
	c.mu.RUnlock()

	if !exists {
		return fmt.Errorf("torrent not found")
	}

	if entry.State().Status() != state.Downloading {
		return nil
	}

	go func() {
		entry.Stop()
		<-c.activeTorrents
	}()

	return nil
}

func (c *Client) ListenForInboundPeers() error {
	slog.Info("listening for incoming peers", "port", c.config.ListenPort)

	ln, err := net.Listen("tcp4", fmt.Sprintf(":%d", c.config.ListenPort))
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

func (c *Client) ActiveTorrents() int {
	var active int
	for _, t := range c.torrents {
		if t.State().Status() == state.Downloading {
			active++
		}
	}

	return active
}

func (c *Client) QueuedTorrents() int {
	var queued int
	for _, t := range c.torrents {
		if t.State().Status() == state.Queued {
			queued++
		}
	}

	return queued
}
