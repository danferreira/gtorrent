package storage

import (
	"bytes"
	"crypto/sha1"
	"io"
	"log/slog"

	"github.com/danferreira/gtorrent/internal/bitfield"
	"github.com/danferreira/gtorrent/internal/metadata"
)

type Manager struct {
	storage io.ReaderAt
}

func NewManager(storage io.ReaderAt) *Manager {
	return &Manager{
		storage: storage,
	}
}

func (mgr *Manager) ScanDisk(m *metadata.Metadata) bitfield.Bitfield {
	pieceHashes := m.Info.Pieces
	pieceLength := m.Info.PieceLength
	torrentSize := m.Info.TotalLength()
	bitfieldSize := (len(pieceHashes) + 7) / 8
	bitfield := make(bitfield.Bitfield, bitfieldSize)

	for index, ph := range pieceHashes {
		begin := int64(index * pieceLength)
		end := begin + int64(pieceLength)

		if end > torrentSize {
			end = torrentSize
		}

		actualPieceLength := end - begin

		buf := make([]byte, actualPieceLength)

		_, err := mgr.storage.ReadAt(buf, begin)
		if err != nil {
			slog.Error("scan disk error", "error", err)
			continue
		}

		if checkIntegrity(ph, buf) {
			bitfield.SetPiece(index)
		}
	}

	return bitfield
}

func checkIntegrity(expectedHash [20]byte, data []byte) bool {
	hash := sha1.Sum(data)
	return bytes.Equal(hash[:], expectedHash[:])
}
