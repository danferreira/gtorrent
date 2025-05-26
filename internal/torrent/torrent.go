package torrent

import (
	"context"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/danferreira/gtorrent/internal/metadata"
	"github.com/danferreira/gtorrent/internal/peer"
	"github.com/danferreira/gtorrent/internal/piece"
	"github.com/danferreira/gtorrent/internal/tracker"
)

type Torrent struct {
	mu             sync.Mutex
	Metadata       metadata.Metadata
	Stats          *piece.TorrentStats
	PeerID         [20]byte
	connectedPeers map[string]*peer.Connection
	SeedingOnly    bool

	trackerManager *tracker.Manager
	pieceManager   *piece.Manager

	listenPort int
}

func NewTorrent(m *metadata.Metadata, peerID [20]byte, listenPort int) (*Torrent, error) {
	torrentSize := m.Info.TotalLength()
	stats := &piece.TorrentStats{
		Downloaded: 0,
		Uploaded:   0,
		Left:       int64(torrentSize),
		Size:       int64(torrentSize),
	}

	connectedPeers := make(map[string]*peer.Connection)

	trackerManager := tracker.NewManager(m, peerID, listenPort)
	pieceManager := piece.NewManager(m)

	return &Torrent{
		Metadata:       *m,
		PeerID:         peerID,
		Stats:          stats,
		connectedPeers: connectedPeers,
		trackerManager: trackerManager,
		pieceManager:   pieceManager,
	}, nil
}

func (t *Torrent) Start(ctx context.Context) error {
	peersChan := t.trackerManager.Run(ctx, t.Stats.Snapshot)
	workChan, failChan, resultChan := t.pieceManager.Run(ctx)

	for {
		select {
		case ps := <-peersChan:
			if !t.SeedingOnly {
				slog.Info("Connecting to peers")
				for _, peer := range ps {
					peerAddr := peer.Addr
					if peerAddr == "127.0.0.1:6881" {
						slog.Info("Own address. Skipping...")
						continue
					}

					if _, connected := t.connectedPeers[peerAddr]; connected {
						slog.Info("Peer is already connected", "peer", peerAddr)
						continue
					}

					go t.newPeerWorker(ctx, peer, workChan, failChan, resultChan)
				}
			}
		case <-ctx.Done():
			slog.Info("Context done")
			return nil
		}
	}
}

func (t *Torrent) NewPeerConn(ctx context.Context, conn net.Conn, workChan <-chan *piece.PieceWork, failChan chan<- *piece.PieceWork, resultChan chan *piece.PieceDownloaded) {
	slog.Info("New peer connection", "peer", conn.RemoteAddr().String())
	p := peer.Peer{
		Addr: conn.RemoteAddr().String(),
	}

	pc := &peer.Connection{
		Peer:        &p,
		InfoHash:    t.Metadata.Info.InfoHash,
		PeerID:      t.PeerID,
		PieceLength: t.Metadata.Info.PieceLength,
	}

	err := pc.AcceptConnection(conn)
	if err != nil {
		slog.Error("Error when accepting connection", "error", err)
	}

	err = pc.ExchangeMessages(ctx, workChan, failChan, resultChan)
	if err != nil {
		slog.Error("Error when exchanging messages with peer", "error", err)
	}

}

func (t *Torrent) StatsChan() <-chan piece.TorrentStats {
	statsChann := make(chan piece.TorrentStats)

	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				statsChann <- *t.Stats
			}
		}

	}()

	return statsChann
}

func (t *Torrent) newPeerWorker(ctx context.Context, p peer.Peer, workChan <-chan *piece.PieceWork, failChan chan<- *piece.PieceWork, resultChan chan *piece.PieceDownloaded) {
	pc := &peer.Connection{
		Peer:        &p,
		InfoHash:    t.Metadata.Info.InfoHash,
		PeerID:      t.PeerID,
		PieceLength: t.Metadata.Info.PieceLength,
	}

	if err := pc.Connect(); err != nil {
		slog.Error("Error during connection", "error", err)
		slog.Info("Removing from list of connected peers", "peer", p.Addr)
		return
	}

	t.addConnectedPeer(pc)

	err := pc.ExchangeMessages(ctx, workChan, failChan, resultChan)
	if err != nil {
		slog.Error("Error when exchanging messages with peer", "error", err)
		t.removeConnectedPeer(&p)
	}
}

func (t *Torrent) addConnectedPeer(pc *peer.Connection) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.connectedPeers[pc.Peer.Addr] = pc
}

func (t *Torrent) removeConnectedPeer(peer *peer.Peer) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.connectedPeers, peer.Addr)
}

// func (t *Torrent) broadcastHavePiece(index int) {
// 	t.mu.Lock()
// 	defer t.mu.Unlock()
// 	for _, pc := range t.connectedPeers {
// 		if err := pc.SendHave(index); err != nil {
// 			slog.Error("Error sending Have message to peer", "error", err)
// 		}
// 	}
// }
