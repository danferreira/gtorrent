package piece

import (
	"bytes"
	"context"
	"crypto/sha1"
	"log/slog"
	"math"

	"github.com/danferreira/gtorrent/internal/metadata"
	"github.com/danferreira/gtorrent/internal/storage"
)

type Manager struct {
	pieceHashes [][20]byte
	pieceLength int
	torrentSize int
	bitfield    *Bitfield
	stats       *TorrentStats
	storage     *storage.Storage
}

func NewManager(m *metadata.Metadata) (*Manager, error) {
	pieceHashes := m.Info.Pieces
	pieceLength := m.Info.PieceLength
	torrentSize := m.Info.TotalLength()

	bitfieldSize := int(math.Ceil(float64(len(pieceHashes)) / 8))
	bitfield := make(Bitfield, bitfieldSize)

	stats := &TorrentStats{
		Downloaded: 0,
		Uploaded:   0,
		Left:       int64(torrentSize),
		Size:       int64(torrentSize),
	}

	storage, err := storage.NewStorage(m.Info.Files)
	if err != nil {
		return nil, err
	}

	return &Manager{
		pieceHashes: pieceHashes,
		pieceLength: pieceLength,
		torrentSize: torrentSize,

		bitfield: &bitfield,
		stats:    stats,
		storage:  storage,
	}, nil
}

func (m *Manager) Run(ctx context.Context) (workChan <-chan *PieceWork, failChan chan<- *PieceWork, resultChan chan *PieceDownloaded) {
	work := make(chan *PieceWork, len(m.pieceHashes))
	fails := make(chan *PieceWork)
	results := make(chan *PieceDownloaded)

	go func() {
		defer close(work)
		defer close(results)

		pending := make(map[int]*PieceWork)

		for index, ph := range m.pieceHashes {
			begin := index * m.pieceLength
			end := begin + m.pieceLength

			if end > m.torrentSize {
				end = m.torrentSize
			}

			actualPieceLength := end - begin

			data, err := m.storage.Read(begin, actualPieceLength)
			if err != nil {
				slog.Error("Disk check error", "error", err)
			} else if m.checkIntegrity(ph, data) {
				m.bitfield.SetPiece(index)
				amount := int64(len(data))
				m.stats.Downloaded += amount
				m.stats.Left -= amount
				continue
			}

			pw := &PieceWork{Index: index, Hash: ph, Length: actualPieceLength}

			pending[pw.Index] = pw

			select {
			case work <- pw:
			case <-ctx.Done():
				return
			}
		}

		slog.Info("Initial work distributed")

		for {
			select {
			case <-ctx.Done():
				slog.Info("PieceManager: context canceled, shutting down")
				return

			case failed := <-fails:
				slog.Warn("Requeueing failed piece", "index", failed.Index)
				pending[failed.Index] = failed

				select {
				case work <- failed:
				case <-ctx.Done():
					return
				}

			case pd := <-results:
				slog.Info("Received completed piece", "index", pd.Index)
				start := pd.Index * m.pieceLength
				if err := m.storage.Write(start, pd.Data); err != nil {
					slog.Error("Error writing piece to disk", "error", err)
					failChan <- &pd.PW
					continue
				}

				delete(pending, pd.Index)

				// Update stats
				m.stats.UpdateDownloaded(int64(len(pd.Data)))

				if len(pending) == 0 {
					slog.Info("All pieces downloaded")
					_ = m.storage.CloseFiles()
					// t.trackerManager.SendAnnouncement(ctx, tracker.EventCompleted, snap)
					// t.SeedingOnly = true
					return
				}
			}
		}

	}()

	return work, fails, results
}

func (m *Manager) checkIntegrity(expectedHash [20]byte, data []byte) bool {
	hash := sha1.Sum(data)
	return bytes.Equal(hash[:], expectedHash[:])
}
