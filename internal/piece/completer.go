package piece

import (
	"context"
	"log/slog"

	"github.com/danferreira/gtorrent/internal/metadata"
	"github.com/danferreira/gtorrent/internal/storage"
)

type Completer struct {
	storage     *storage.Storage
	pieceLength int
}

func NewCompleter(m *metadata.Metadata, storage *storage.Storage) *Completer {
	return &Completer{storage: storage, pieceLength: m.Info.PieceLength}
}

func (c *Completer) Run(ctx context.Context, failChan chan<- *PieceWork, resultChan <-chan *PieceDownloaded) {
	for {
		select {
		case p := <-resultChan:
			slog.Info("Received completed piece", "index", p.Index)
			start := p.Index * c.pieceLength
			_, err := c.storage.WriteAt(p.Data, int64(start))
			if err != nil {
				slog.Error("Error writing piece to disk", "error", err)

				failChan <- p.PW
				continue
			}
			slog.Info("Piece written to disk", "index", p.Index)
		case <-ctx.Done():
			slog.Info("Completer context done, shutting down")
			return
		}
	}
}
