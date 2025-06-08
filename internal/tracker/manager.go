package tracker

import (
	"context"
	"log/slog"
	"time"

	"github.com/danferreira/gtorrent/internal/peer"
)

type TrackerAnnouncer interface {
	Announce(ctx context.Context, e Event, downloaded, uploaded, left int64) ([]peer.Peer, int, error)
}

type Manager struct {
	tracker TrackerAnnouncer
}

func NewManager(tracker TrackerAnnouncer) *Manager {
	return &Manager{
		tracker: tracker,
	}
}

type Snapshot func() (int64, int64, int64)

func (m *Manager) Run(ctx context.Context, snapshotFn Snapshot, pool *peer.Pool) {
	slog.Info("Starting announce worker")

	currentEvent := EventStarted

	for {
		peers, interval, err := m.SendAnnouncement(ctx, currentEvent, snapshotFn)

		if err != nil {
			slog.Error("Error on tracker announce. Retrying in 10 seconds...", "error", err)
			interval = 10
		} else {
			slog.Info("Successfully announced to tracker")
			pool.PushMany(peers)
			currentEvent = EventUpdated
		}

		select {
		case <-time.After(time.Duration(interval) * time.Second):
		case <-ctx.Done():
			_, _, err := m.SendAnnouncement(ctx, EventStopped, snapshotFn)
			if err != nil {
				slog.Error("Error on sending stop event to tracker", "error", err)
			}
			return
		}
	}
}

func (m *Manager) SendAnnouncement(ctx context.Context, event Event, snapshotFn Snapshot) ([]peer.Peer, int, error) {
	slog.Info("Sending announcement to tracker", "event", event)
	downloaded, uploaded, left := snapshotFn()
	receivedPeers, interval, err := m.tracker.Announce(ctx, event, downloaded, uploaded, left)
	if err != nil {
		return nil, 0, err
	}

	return receivedPeers, interval, err
}
