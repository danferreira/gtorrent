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
	mu             sync.Mutex
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
	resultChan := make(chan *piece.PieceDownloaded, 100) // buffered channel for results

	if !m.state.IsCompleted() {
		go m.outboundConnections(ctx, workChan, failChan, resultChan)
	}

	go func() {
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
			}
		}
	}()

	return resultChan
}

func (m *Manager) outboundConnections(ctx context.Context, workChan <-chan *piece.PieceWork, failChan chan<- *piece.PieceWork, resultChan chan<- *piece.PieceDownloaded) {
	ticker := time.NewTicker(m.poolInterval)

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

	c := newConnection(p, m.pieceLength, m.state.GetBitfield(), m.storage)
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
