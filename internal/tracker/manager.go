package tracker

import (
	"context"
	"log/slog"
	"time"

	"github.com/danferreira/gtorrent/internal/peer"
	"github.com/danferreira/gtorrent/internal/state"
)

type TrackerAnnouncer interface {
	Announce(ctx context.Context, e Event, downloaded, uploaded, left int64) ([]peer.Peer, int, error)
}

type Manager struct {
	tracker TrackerAnnouncer
	pool    *peer.Pool
}

func NewManager(tracker TrackerAnnouncer, pool *peer.Pool) *Manager {
	return &Manager{
		tracker: tracker,
		pool:    pool,
	}
}

type SnapshotFn func() state.Stats

func (m *Manager) Run(ctx context.Context, snapshotFn SnapshotFn) {
	slog.Info("starting announce worker")

	currentEvent := EventStarted

	for {
		peers, interval, err := m.sendAnnouncement(ctx, currentEvent, snapshotFn)

		if err != nil {
			slog.Error("error on tracker announce. Retrying in 10 seconds...", "error", err)
			interval = 10
		} else {
			slog.Info("successfully announced to tracker")
			m.pool.PushMany(peers)
			currentEvent = EventUpdated
		}

		select {
		case <-time.After(time.Duration(interval) * time.Second):
		case <-ctx.Done():
			newContext, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()

			_, _, err := m.sendAnnouncement(newContext, EventStopped, snapshotFn)
			if err != nil {
				slog.Error("error on sending stop event to tracker", "error", err)
			}

			return
		}
	}
}

func (m *Manager) sendAnnouncement(ctx context.Context, event Event, snapshotFn SnapshotFn) ([]peer.Peer, int, error) {
	slog.Info("sending announcement to tracker", "event", event)
	snapshot := snapshotFn()
	receivedPeers, interval, err := m.tracker.Announce(ctx, event, snapshot.Downloaded, snapshot.Uploaded, snapshot.Left)
	if err != nil {
		return nil, 0, err
	}

	return receivedPeers, interval, nil
}
