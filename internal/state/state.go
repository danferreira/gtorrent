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

type State struct {
	mu       sync.RWMutex
	Bitfield bitfield.Bitfield

	downloaded int64
	uploaded   int64
	size       int64
	Left       int64
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

		downloaded: downloaded,
		uploaded:   uploaded,
		Left:       left,
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
		s.downloaded += int64(dataSize)
		s.Left -= int64(dataSize)
	}
}

func (s *State) IsCompleted() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Left == 0
}

func (s *State) Snapshot() (int64, int64, int64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.downloaded, s.uploaded, s.Left
}

func checkIntegrity(expectedHash [20]byte, data []byte) bool {
	hash := sha1.Sum(data)
	return bytes.Equal(hash[:], expectedHash[:])
}
