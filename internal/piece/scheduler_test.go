package piece

import (
	"context"
	"testing"

	"github.com/danferreira/gtorrent/internal/bitfield"
	"github.com/danferreira/gtorrent/internal/metadata"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func NewMockedScheduler() (*Scheduler, error) {
	m := &metadata.Metadata{
		Info: metadata.Info{
			PieceLength: 262144,
			Pieces:      [][20]byte{{0}, {0}, {0}, {0}},
			Files: []metadata.FileInfo{
				{Length: 1048576, Path: "file1.txt"},
			},
		},
	}
	bitfield := make(bitfield.Bitfield, 1)

	bitfield.SetPiece(0)
	bitfield.SetPiece(1)

	return NewScheduler(m, bitfield)
}

func TestNewScheduler(t *testing.T) {
	scheduler, err := NewMockedScheduler()
	if err != nil {
		t.Fatalf("Failed to create scheduler: %v", err)
	}
	if len(scheduler.pending) != 2 {
		t.Errorf("Expected 2 pending pieces, got %d", len(scheduler.pending))
	}
}

func TestRun(t *testing.T) {
	scheduler, err := NewMockedScheduler()
	if err != nil {
		t.Fatalf("Failed to create scheduler: %v", err)
	}

	workChan, failChan := scheduler.Run(context.Background())
	var pw *PieceWork
	pw = <-workChan
	require.Equal(t, 2, pw.Index)
	pw = <-workChan
	require.Equal(t, 3, pw.Index)
	require.Equal(t, 0, len(scheduler.pending))

	failChan <- pw
	npw := <-workChan

	assert.Equal(t, pw, npw)
}
