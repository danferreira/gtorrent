package peer

import (
	"context"
	"log/slog"
	"net"
	"sync"

	"github.com/danferreira/gtorrent/internal/handshake"
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

func (m *Manager) OutboundConnection(ctx context.Context, p Peer, workChan <-chan *piece.PieceWork, failChan chan<- *piece.PieceWork, resultChan chan *piece.PieceDownloaded) {
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

func (m *Manager) InboundConnection(ctx context.Context, conn net.Conn, workChan <-chan *piece.PieceWork, failChan chan<- *piece.PieceWork, resultChan chan *piece.PieceDownloaded) error {
	hs := handshake.Handshake{
		InfoHash: m.infoHash,
		PeerID:   m.peerID,
	}

	err := hs.Write(conn)
	if err != nil {
		return err
	}

	slog.Info("New peer connection", "peer", conn.RemoteAddr().String())
	p := Peer{
		Addr: conn.RemoteAddr().String(),
	}

	pc := &Connection{
		Peer:        &p,
		InfoHash:    m.infoHash,
		PeerID:      m.peerID,
		PieceLength: m.pieceLength,
	}

	err = pc.AcceptConnection(conn)
	if err != nil {
		slog.Error("Error when accepting connection", "error", err)
	}

	err = pc.ExchangeMessages(ctx, workChan, failChan, resultChan)
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
