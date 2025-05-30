package torrent

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/danferreira/gtorrent/internal/metadata"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type MockTorrentRunner struct {
	Metadata  metadata.Metadata
	StartFunc func(ctx context.Context) error
}

func (m MockTorrentRunner) Start(ctx context.Context) error {
	if m.StartFunc != nil {
		return m.StartFunc(ctx)
	}
	return nil
}

type MockTorrentFactory struct {
	NewTorrentFunc func(m *metadata.Metadata, peerID [20]byte, listenPort int) (TorrentRunner, error)
}

func (m MockTorrentFactory) NewTorrent(meta *metadata.Metadata, peerID [20]byte, listenPort int) (TorrentRunner, error) {
	if m.NewTorrentFunc != nil {
		return m.NewTorrentFunc(meta, peerID, listenPort)
	}
	return MockTorrentRunner{}, nil
}

func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(
		slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}),
	))

	os.Exit(m.Run())
}

func TestGeneratePeerID(t *testing.T) {
	peerID := generatePeerID()

	assert.Equal(t, 20, len(peerID))
	assert.True(t, strings.HasPrefix(string(peerID[:]), "-GT0001-"))
}

func TestAddFile(t *testing.T) {
	mockFactory := MockTorrentFactory{}

	client := NewClientWithDeps(mockFactory, 6881)

	err := client.AddFile("invalid.torrent")

	expectedError := "failed to parse torrent file invalid.torrent: open invalid.torrent: no such file or directory"
	require.EqualError(t, err, expectedError)

	err = client.AddFile("testdata/file.torrent")
	assert.Nil(t, err)
	assert.Equal(t, 1, len(client.torrents))

	err = client.AddFile("testdata/file.torrent")
	assert.EqualError(t, err, "torrent already added: testdata/file.torrent")
	assert.Equal(t, 1, len(client.torrents))
}

func TestStartTorrent(t *testing.T) {
	startCalled := make(chan bool, 1)

	mockTorrent := MockTorrentRunner{
		StartFunc: func(ctx context.Context) error {
			startCalled <- true
			return nil
		},
	}
	mockFactory := MockTorrentFactory{
		NewTorrentFunc: func(m *metadata.Metadata, peerID [20]byte, listenPort int) (TorrentRunner, error) {
			mockTorrent.Metadata = *m
			return mockTorrent, nil
		},
	}
	client := NewClientWithDeps(mockFactory, 6881)

	err := client.AddFile("testdata/file.torrent")
	assert.Nil(t, err)

	unknownHash := [20]byte{}
	err = client.StartTorrent(unknownHash)
	require.EqualError(t, err, "torrent not found")

	client.StartTorrent(mockTorrent.Metadata.InfoHash())

	select {
	case <-startCalled:
		// Success - Start was called
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Start method was not called on torrent")
	}
}
