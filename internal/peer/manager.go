package peer

import (
	"context"
	"log/slog"
	"net"
	"sync"

	"github.com/danferreira/gtorrent/internal/metadata"
	"github.com/danferreira/gtorrent/internal/piece"
)

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

func (m *Manager) Run(ctx context.Context, peerChan <-chan Peer, workChan <-chan *piece.PieceWork, failChan chan<- *piece.PieceWork, resultChan chan<- *piece.PieceDownloaded) {
	go func() {
		for {
			select {
			case p := <-peerChan:
				slog.Info("Connecting to peer")
				// TODO: Fix this hardcoded address in the future
				if p.Addr == "127.0.0.1:6881" {
					slog.Info("Own address. Skipping...")
					continue
				}

				go m.outboundConnection(ctx, p, workChan, failChan, resultChan)

			case <-ctx.Done():
				slog.Info("Context done")
			}
		}
	}()
}

func (m *Manager) outboundConnection(ctx context.Context, p Peer, workChan <-chan *piece.PieceWork, failChan chan<- *piece.PieceWork, resultChan chan<- *piece.PieceDownloaded) {
	pc := &Connection{
		Peer:        &p,
		InfoHash:    m.infoHash,
		PeerID:      m.peerID,
		PieceLength: m.pieceLength,
	}

	if err := pc.Connect(); err != nil {
		slog.Error("Error during connection", "error", err)
		slog.Info("Removing from list of connected peers", "peer", p.Addr)
		return
	}

	m.addConnectedPeer(pc)

	err := pc.ExchangeMessages(ctx, workChan, failChan, resultChan)
	if err != nil {
		slog.Error("Error when exchanging messages with peer", "error", err)
		m.removeConnectedPeer(&p)
	}
}

func (m *Manager) InboundConnection(ctx context.Context, conn net.Conn, workChan <-chan *piece.PieceWork, failChan chan<- *piece.PieceWork, downloadedChan chan<- *piece.PieceDownloaded) error {
	slog.Info("New inbound connection", "addr", conn.RemoteAddr().String())
	p := Peer{
		Addr: conn.RemoteAddr().String(),
	}

	pc := NewConnection(p, m.infoHash, m.peerID, m.pieceLength)
	err := pc.AcceptConnection(conn)
	if err != nil {
		slog.Error("Error when accepting connection", "error", err)
	}

	err = pc.ExchangeMessages(ctx, workChan, failChan, downloadedChan)
	if err != nil {
		slog.Error("Error when exchanging messages with peer", "error", err)
	}

	return nil
}

func (m *Manager) addConnectedPeer(pc *Connection) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connectedPeers[pc.Peer.Addr] = pc
}

func (m *Manager) removeConnectedPeer(peer *Peer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.connectedPeers, peer.Addr)
}
