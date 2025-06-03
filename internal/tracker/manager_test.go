package tracker

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/danferreira/gtorrent/internal/peer"
	"github.com/danferreira/gtorrent/internal/piece"
	"github.com/stretchr/testify/assert"
)

type MockTrackerService struct {
	peersToReturn []peer.Peer
	interval      int
	err           error
	announceCalls int
	lastEvent     Event
}

func (mt *MockTrackerService) Announce(ctx context.Context, e Event, downloaded, uploaded, left int64) ([]peer.Peer, int, error) {
	mt.announceCalls++
	mt.lastEvent = e
	return mt.peersToReturn, mt.interval, mt.err
}

func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(
		slog.NewTextHandler(io.Discard, nil),
	))

	os.Exit(m.Run())
}

func TestRun(t *testing.T) {
	mockTracker := &MockTrackerService{
		peersToReturn: []peer.Peer{
			{Addr: "1.1.1.1:1234"},
			{Addr: "2.2.2.2:5678"},
		},
		interval: 1,
	}

	m := NewManager(mockTracker)

	snapshotFn := func() piece.Snapshot {
		return piece.Snapshot{Downloaded: 0, Uploaded: 0, Left: 100}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	peersChan := m.Run(ctx, snapshotFn)

	peers := <-peersChan

	assert.Equal(t, 1, mockTracker.announceCalls)
	assert.True(t, len(peers) > 0)
	assert.Equal(t, mockTracker.peersToReturn, peers)
	assert.Equal(t, EventStarted, mockTracker.lastEvent)

	peers = <-peersChan
	assert.Equal(t, 2, mockTracker.announceCalls)
	assert.Equal(t, EventUpdated, mockTracker.lastEvent)

	peers = <-peersChan
	assert.Equal(t, 3, mockTracker.announceCalls)
	assert.Equal(t, EventStopped, mockTracker.lastEvent)

	_, ok := <-peersChan
	assert.False(t, ok, "expected peersChan to be closed after context timeout")
}
