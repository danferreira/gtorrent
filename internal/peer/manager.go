package peer

import (
	"context"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/danferreira/gtorrent/internal/metadata"
	"github.com/danferreira/gtorrent/internal/piece"
	"github.com/danferreira/gtorrent/internal/state"
	"github.com/danferreira/gtorrent/internal/storage"
)

type Manager struct {
	mu             sync.RWMutex
	connectedPeers map[string]*Connection
	infoHash       [20]byte
	peerID         [20]byte
	pieceLength    int
	state          *state.State
	storage        *storage.Storage
	pool           *Pool
	maxPeers       int
	poolInterval   time.Duration
}

func NewManager(m *metadata.Metadata, state *state.State, peerID [20]byte, pool *Pool, storage *storage.Storage) *Manager {
	return &Manager{
		infoHash:       m.Info.InfoHash,
		peerID:         peerID,
		connectedPeers: make(map[string]*Connection),

		pieceLength: m.Info.PieceLength,
		state:       state,
		storage:     storage,

		pool:         pool,
		maxPeers:     15,
		poolInterval: 5 * time.Second,
	}
}

func (m *Manager) Run(ctx context.Context, workChan <-chan *piece.PieceWork, failChan chan<- *piece.PieceWork, inboundConnections <-chan net.Conn) <-chan *piece.PieceDownloaded {
	downloadedChan := make(chan *piece.PieceDownloaded, 100) // buffered channel for results

	if !m.state.IsCompleted() {
		go m.outboundConnections(ctx, workChan, failChan, downloadedChan)
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case c := <-inboundConnections:
				go func() {
					pc, err := m.inboundConnection(c)
					if err != nil {
						slog.Error("an error occurred while accepting a peer connection", "error", err)
						c.Close()
					}

					pc.start(ctx, workChan, failChan, downloadedChan)
				}()
			}
		}
	}()

	return downloadedChan
}

func (m *Manager) RunSeeding(ctx context.Context, inboundConnections <-chan net.Conn) {
	for {
		select {
		case <-ctx.Done():
			return
		case c := <-inboundConnections:
			go func() {
				pc, err := m.inboundConnection(c)
				if err != nil {
					slog.Error("an error occurred while accepting a peer connection", "error", err)
					c.Close()
				}

				pc.startSeeding(ctx)
			}()
		}
	}

}

func (m *Manager) outboundConnections(ctx context.Context, workChan <-chan *piece.PieceWork, failChan chan<- *piece.PieceWork, resultChan chan<- *piece.PieceDownloaded) {
	ticker := time.NewTicker(m.poolInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if m.state.IsCompleted() {
				return
			}

			// fill up to MaxPeers
			if m.countPeers() < m.maxPeers {
				peer, ok := m.pool.Pop()
				if ok {
					if peer.Addr != "127.0.0.1:6881" {
						go m.outboundConnection(ctx, peer, workChan, failChan, resultChan)
					}
				}
			}
		}
	}
}

func (m *Manager) outboundConnection(ctx context.Context, p Peer, workChan <-chan *piece.PieceWork, failChan chan<- *piece.PieceWork, downloadedChan chan<- *piece.PieceDownloaded) {
	c := newConnection(&p, m.pieceLength, m.state.GetBitfield(), m.storage)
	if err := c.dial(ctx, m.infoHash, m.peerID); err != nil {
		slog.Error("error during connection", "error", err)
		return
	}

	m.addConnectedPeer(c)

	c.start(ctx, workChan, failChan, downloadedChan)
}

func (m *Manager) inboundConnection(conn net.Conn) (*Connection, error) {
	slog.Info("new inbound connection", "addr", conn.RemoteAddr().String())
	p := &Peer{
		Addr: conn.RemoteAddr().String(),
	}

	c := newConnection(p, m.pieceLength, m.state.GetBitfield(), m.storage)
	if err := c.accept(conn, m.infoHash, m.peerID); err != nil {
		return nil, err
	}

	m.addConnectedPeer(c)

	return c, nil
}

func (m *Manager) addConnectedPeer(pc *Connection) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.connectedPeers[pc.peer.Addr] = pc
	m.state.IncreasePeers()

	go func() {
		<-pc.done // blocks until connection exits
		m.removePeer(pc.peer.Addr)
	}()
}

func (m *Manager) removePeer(addr string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.connectedPeers, addr)
	m.state.DecreasePeers()
}

func (m *Manager) countPeers() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.connectedPeers)
}
