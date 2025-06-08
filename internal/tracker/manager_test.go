package tracker

import (
	"context"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/danferreira/gtorrent/internal/peer"
	"github.com/stretchr/testify/assert"
)

type MockTrackerService struct {
	peers    []peer.Peer
	interval int
	err      error

	mu        sync.Mutex
	calls     int
	lastEvent Event
}

func (m *MockTrackerService) Announce(ctx context.Context, e Event, downloaded, uploaded, left int64) ([]peer.Peer, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	m.lastEvent = e
	return m.peers, m.interval, m.err
}

func (m *MockTrackerService) stats() (calls int, last Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls, m.lastEvent
}

func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(
		slog.NewTextHandler(io.Discard, nil),
	))

	os.Exit(m.Run())
}

func TestRun(t *testing.T) {
	mt := &MockTrackerService{
		peers: []peer.Peer{
			{Addr: "1.1.1.1:1234"},
			{Addr: "2.2.2.2:5678"},
		},
		interval: 1,
	}

	snapshotFn := func() (int64, int64, int64) {
		return 0, 0, 100
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	pool := peer.NewPool(10)

	m := NewManager(mt)
	go m.Run(ctx, snapshotFn, pool)

	// --- first announce (started) must happen quickly ---------------
	assert.Eventually(t, func() bool {
		calls, ev := mt.stats()
		return calls == 1 && ev == EventStarted && pool.Len() == 2
	}, 200*time.Millisecond, 10*time.Millisecond)

	// --- second announce (updated) after ~1 s -----------------------
	assert.Eventually(t, func() bool {
		calls, ev := mt.stats()
		return calls >= 2 && ev == EventUpdated
	}, 1250*time.Millisecond, 20*time.Millisecond)

	// --- context cancel triggers stopped announce -------------------
	assert.Eventually(t, func() bool {
		_, ev := mt.stats()
		return ev == EventStopped
	}, 500*time.Millisecond, 10*time.Millisecond)
}
