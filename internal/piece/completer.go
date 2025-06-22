package piece

import (
	"context"
	"log/slog"

	"github.com/danferreira/gtorrent/internal/metadata"
	"github.com/danferreira/gtorrent/internal/state"
	"github.com/danferreira/gtorrent/internal/storage"
)

type Completer struct {
	storage     *storage.Storage
	pieceLength int
	state       *state.State
}

func NewCompleter(m *metadata.Metadata, state *state.State, storage *storage.Storage) *Completer {
	return &Completer{storage: storage, pieceLength: m.Info.PieceLength, state: state}
}

func (c *Completer) Run(ctx context.Context, failChan chan<- *PieceWork, resultChan <-chan *PieceDownloaded) {
	for {
		select {
		case p := <-resultChan:
			slog.Debug("received completed piece", "index", p.Index)
			start := p.Index * c.pieceLength
			_, err := c.storage.WriteAt(p.Data, int64(start))
			if err != nil {
				slog.Error("error writing piece to disk", "error", err)

				failChan <- p.PW
				continue
			}

			slog.Debug("piece written to disk", "index", p.Index)
			c.state.SetPieceCompleted(p.Index, p.PW.Length)
		case <-ctx.Done():
			slog.Info("completer context done, shutting down")
			return
		}
	}
}
