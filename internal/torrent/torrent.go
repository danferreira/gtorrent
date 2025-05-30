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
	mu          sync.Mutex
	Metadata    metadata.Metadata
	Stats       *piece.TorrentStats
	PeerID      [20]byte
	SeedingOnly bool

	inboundConnections chan net.Conn

	trackerManager *tracker.Manager
	pieceManager   *piece.Manager
	peerManager    *peer.Manager

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

	trackerManager := tracker.NewManager(m, peerID, listenPort)
	pieceManager, err := piece.NewManager(m)
	if err != nil {
		return nil, err
	}
	peerManager := peer.NewManager(m, peerID)

	inboundConnections := make(chan net.Conn)

	return &Torrent{
		Metadata:           *m,
		PeerID:             peerID,
		Stats:              stats,
		trackerManager:     trackerManager,
		pieceManager:       pieceManager,
		peerManager:        peerManager,
		inboundConnections: inboundConnections,
	}, nil
}

func (t *Torrent) Start(ctx context.Context) error {
	peersChan := t.trackerManager.Run(ctx, t.Stats.Snapshot)
	workChan, failChan, resultChan := t.pieceManager.Run(ctx)

	defer close(t.inboundConnections)
	for {
		select {
		case c := <-t.inboundConnections:
			go func() {
				err := t.peerManager.InboundConnection(ctx, c, workChan, failChan, resultChan)
				if err != nil {
					c.Close()
				}
			}()
		case ps := <-peersChan:
			if !t.SeedingOnly {
				slog.Info("Connecting to peers")
				for _, peer := range ps {
					peerAddr := peer.Addr
					if peerAddr == "127.0.0.1:6881" {
						slog.Info("Own address. Skipping...")
						continue
					}

					go t.peerManager.OutboundConnection(ctx, peer, workChan, failChan, resultChan)
				}
			}
		case <-ctx.Done():
			slog.Info("Context done")
			return nil
		}
	}
}

func (t *Torrent) NewInboundConnection(conn net.Conn) {
	t.inboundConnections <- conn
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

// func (t *Torrent) broadcastHavePiece(index int) {
// 	t.mu.Lock()
// 	defer t.mu.Unlock()
// 	for _, pc := range t.connectedPeers {
// 		if err := pc.SendHave(index); err != nil {
// 			slog.Error("Error sending Have message to peer", "error", err)
// 		}
// 	}
// }
