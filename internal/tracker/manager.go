package tracker

import (
	"context"
	"log/slog"
	"time"

	"github.com/danferreira/gtorrent/internal/metadata"
	"github.com/danferreira/gtorrent/internal/peer"
	"github.com/danferreira/gtorrent/internal/piece"
)

type Manager struct {
	tracker *Tracker
}

func NewManager(m *metadata.Metadata, peerID [20]byte, listenPort int) *Manager {
	t := NewTracker(m, peerID, listenPort)

	return &Manager{
		tracker: t,
	}
}

func (m *Manager) Run(ctx context.Context, snapshotFn func() piece.Snapshot) <-chan []peer.Peer {
	slog.Info("Starting announce worker")
	peersChan := make(chan []peer.Peer)

	go func() {
		defer close(peersChan)

		currentEvent := EventStarted
		for {
			snap := snapshotFn()
			peers, interval, err := m.SendAnnouncement(ctx, currentEvent, snap)

			if err != nil {
				slog.Error("Error on tracker announce. Retrying in 10 seconds...", "error", err)
				interval = 10
			} else {
				slog.Info("Successfully announced to tracker")
				currentEvent = EventUpdated

				if len(peers) > 0 {
					peersChan <- peers
				}
			}

			select {
			case <-time.After(time.Duration(interval) * time.Second):
			case <-ctx.Done():
				snap := snapshotFn()
				_, _, err := m.SendAnnouncement(ctx, EventStopped, snap)
				if err != nil {
					slog.Error("Error on sending stop event to tracker", "error", err)
				}
				return
			}
		}
	}()

	return peersChan
}

func (m *Manager) SendAnnouncement(ctx context.Context, event Event, snap piece.Snapshot) ([]peer.Peer, int, error) {
	slog.Info("Sending announcement to tracker", "event", event)
	receivedPeers, interval, err := m.tracker.Announce(ctx, event, snap.Downloaded, snap.Uploaded, snap.Left)
	if err != nil {
		return nil, 0, err
	}

	return receivedPeers, interval, err
}
