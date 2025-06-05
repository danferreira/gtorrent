package peer

import (
	"context"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/danferreira/gtorrent/internal/metadata"
	"github.com/danferreira/gtorrent/internal/piece"
)

const MaxPeers = 50

type Manager struct {
	mu             sync.Mutex
	connectedPeers map[string]*Connection
	infoHash       [20]byte
	peerID         [20]byte
	pieceLength    int
}

func NewManager(m *metadata.Metadata, peerID [20]byte) *Manager {
	infoHash := m.Info.InfoHash
	pieceLength := m.Info.PieceLength

	connectedPeers := make(map[string]*Connection)
	return &Manager{
		infoHash:       infoHash,
		peerID:         peerID,
		connectedPeers: connectedPeers,

		pieceLength: pieceLength,
	}
}

func (m *Manager) Run(ctx context.Context, pool *Pool, workChan <-chan *piece.PieceWork, failChan chan<- *piece.PieceWork, resultChan chan<- *piece.PieceDownloaded) {
	ticker := time.NewTicker(5 * time.Second) // housekeeping
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// fill up to MaxPeers
			for len(m.connectedPeers) < MaxPeers {
				peer, ok := pool.Pop()
				if !ok {
					slog.Info("pool is empty")
				} else {
					go m.outboundConnection(ctx, peer, workChan, failChan, resultChan)
				}
			}
		}
	}
}

func (m *Manager) outboundConnection(ctx context.Context, p Peer, workChan <-chan *piece.PieceWork, failChan chan<- *piece.PieceWork, resultChan chan<- *piece.PieceDownloaded) {
	pc := NewConnection(&p, m.pieceLength)

	if err := pc.outboundConnection(m.infoHash, m.peerID); err != nil {
		slog.Error("Error during connection", "error", err)
		return
	}

	if err := pc.sendBitfield(); err != nil {
		slog.Warn("Error sending bitfield", "error", err)
	}
	m.addConnectedPeer(pc)

	err := pc.exchangeMessages(ctx, workChan, failChan, resultChan)
	if err != nil {
		slog.Error("Error when exchanging messages with peer", "error", err)
		m.removeConnectedPeer(&p)
	}
}

func (m *Manager) InboundConnection(ctx context.Context, conn net.Conn, workChan <-chan *piece.PieceWork, failChan chan<- *piece.PieceWork, downloadedChan chan<- *piece.PieceDownloaded) error {
	slog.Info("New inbound connection", "addr", conn.RemoteAddr().String())
	p := &Peer{
		Addr: conn.RemoteAddr().String(),
	}

	pc := NewConnection(p, m.pieceLength)
	err := pc.inboundConnection(conn, m.infoHash, m.peerID)
	if err != nil {
		slog.Error("Error when accepting connection", "error", err)
	}

	err = pc.exchangeMessages(ctx, workChan, failChan, downloadedChan)
	if err != nil {
		slog.Error("Error when exchanging messages with peer", "error", err)
	}

	return nil
}

func (m *Manager) addConnectedPeer(pc *Connection) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connectedPeers[pc.peer.Addr] = pc
}

func (m *Manager) removeConnectedPeer(peer *Peer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.connectedPeers, peer.Addr)
}
