package state

import (
	"bytes"
	"crypto/sha1"
	"errors"
	"io"
	"log/slog"
	"sync"

	"github.com/danferreira/gtorrent/internal/bitfield"
	"github.com/danferreira/gtorrent/internal/metadata"
)

type Status uint8

const (
	Queued  Status = iota
	Stopped Status = iota
	Downloading
)

func (s Status) String() string {
	switch s {
	case Queued:
		return "Queued"
	case Stopped:
		return "Stopped"
	case Downloading:
		return "Downloading"
	}

	return ""
}

type Stats struct {
	Downloaded int64
	Uploaded   int64
	Left       int64
}

type State struct {
	mu       sync.RWMutex
	Bitfield bitfield.Bitfield

	status Status

	peers int
	size  int64

	Stats Stats
}

func NewState(m *metadata.Metadata, storage io.ReaderAt) *State {
	pieceLength := m.Info.PieceLength
	torrentSize := m.Info.TotalLength()
	bitfieldSize := (len(m.Info.Pieces) + 7) / 8
	bitfield := make(bitfield.Bitfield, bitfieldSize)

	var downloaded, uploaded, left int64
	left = torrentSize

	for index, hash := range m.Info.Pieces {
		begin := int64(index * pieceLength)
		end := begin + int64(pieceLength)

		if end > torrentSize {
			end = torrentSize
		}

		actualPieceLength := end - begin
		buf := make([]byte, actualPieceLength)

		_, err := storage.ReadAt(buf, begin)
		if err != nil && !errors.Is(err, io.EOF) {
			slog.Error("disk check error", "error", err)
		} else if checkIntegrity(hash, buf) {
			if !bitfield.HasPiece(index) {
				bitfield.SetPiece(index)

				amount := int64(len(buf))
				downloaded += amount
				left -= amount
			}
			continue
		}
	}

	return &State{
		Bitfield: bitfield,
		Stats: Stats{
			Downloaded: downloaded,
			Uploaded:   uploaded,
			Left:       left,
		},
	}

}

func (s *State) GetBitfield() bitfield.Bitfield {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Return copy to prevent external modification
	bf := make(bitfield.Bitfield, len(s.Bitfield))
	copy(bf, s.Bitfield)
	return bf
}

func (s *State) SetPieceCompleted(index int, dataSize int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.Bitfield.HasPiece(index) {
		s.Bitfield.SetPiece(index)

		s.Stats.Downloaded += int64(dataSize)
		s.Stats.Left -= int64(dataSize)
	}
}

func (s *State) IsCompleted() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Stats.Left == 0
}

func (s *State) Snapshot() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.Stats
}

func (s *State) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.status
}

func (s *State) SetStatus(st Status) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = st
}

func (s *State) Peers() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.peers
}

func (s *State) IncreasePeers() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.peers++
}

func (s *State) DecreasePeers() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.peers > 0 {
		s.peers--
	}
}

func checkIntegrity(expectedHash [20]byte, data []byte) bool {
	hash := sha1.Sum(data)
	return bytes.Equal(hash[:], expectedHash[:])
}
