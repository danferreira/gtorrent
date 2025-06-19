package peer

import (
	"context"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/danferreira/gtorrent/internal/bitfield"
	"github.com/danferreira/gtorrent/internal/metadata"
	"github.com/danferreira/gtorrent/internal/piece"
	"github.com/danferreira/gtorrent/internal/storage"
)

type Manager struct {
	mu             sync.Mutex
	connectedPeers map[string]*Connection
	infoHash       [20]byte
	peerID         [20]byte
	pieceLength    int
	bitfield       *bitfield.Bitfield
	storage        *storage.Storage
	pool           *Pool
	maxPeers       int
}

func NewManager(m *metadata.Metadata, bitfield *bitfield.Bitfield, peerID [20]byte, pool *Pool, storage *storage.Storage) *Manager {
	return &Manager{
		infoHash:       m.Info.InfoHash,
		peerID:         peerID,
		connectedPeers: make(map[string]*Connection),

		pieceLength: m.Info.PieceLength,
		bitfield:    bitfield,
		storage:     storage,

		pool:     pool,
		maxPeers: 15,
	}
}

func (m *Manager) Run(ctx context.Context, workChan <-chan *piece.PieceWork, failChan chan<- *piece.PieceWork, inboundConnections <-chan net.Conn) <-chan *piece.PieceDownloaded {
	resultChan := make(chan *piece.PieceDownloaded, 100) // buffered channel for results

	go func() {
		defer close(resultChan)

		ticker := time.NewTicker(1 * time.Second) // housekeeping
		for {
			select {
			case <-ctx.Done():
				return
			case c := <-inboundConnections:
				go func() {
					if err := m.inboundConnection(ctx, c, workChan, failChan, resultChan); err != nil {
						slog.Error("an error occurred while accepting a peer connection", "error", err)
						c.Close()
					}
				}()
			case <-ticker.C:
				// fill up to MaxPeers
				if m.countPeers() < m.maxPeers {
					peer, ok := m.pool.Pop()
					if ok {
						go m.outboundConnection(ctx, peer, workChan, failChan, resultChan)
					}
				}
			}
		}
	}()

	return resultChan
}

func (m *Manager) outboundConnection(ctx context.Context, p Peer, workChan <-chan *piece.PieceWork, failChan chan<- *piece.PieceWork, downloadedChan chan<- *piece.PieceDownloaded) {
	c := newConnection(&p, m.pieceLength, m.bitfield, m.storage)
	if err := c.dial(m.infoHash, m.peerID); err != nil {
		slog.Error("error during connection", "error", err)
		return
	}

	m.addConnectedPeer(c)

	c.start(ctx, workChan, failChan, downloadedChan)
}

func (m *Manager) inboundConnection(ctx context.Context, conn net.Conn, workChan <-chan *piece.PieceWork, failChan chan<- *piece.PieceWork, downloadedChan chan<- *piece.PieceDownloaded) error {
	slog.Info("new inbound connection", "addr", conn.RemoteAddr().String())
	p := &Peer{
		Addr: conn.RemoteAddr().String(),
	}

	c := newConnection(p, m.pieceLength, m.bitfield, m.storage)
	if err := c.accept(conn, m.infoHash, m.peerID); err != nil {
		return err
	}

	m.addConnectedPeer(c)

	c.start(ctx, workChan, failChan, downloadedChan)

	return nil
}

func (m *Manager) addConnectedPeer(pc *Connection) {
	m.mu.Lock()
	m.connectedPeers[pc.peer.Addr] = pc
	m.mu.Unlock()

	go func() {
		<-pc.done // blocks until connection exits
		m.removePeer(pc.peer.Addr)
	}()
}

func (m *Manager) removePeer(addr string) {
	m.mu.Lock()
	delete(m.connectedPeers, addr)
	m.mu.Unlock()
}

func (m *Manager) countPeers() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.connectedPeers)
}
