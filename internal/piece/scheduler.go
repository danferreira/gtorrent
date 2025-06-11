package piece

import (
	"context"
	"sync"

	"github.com/danferreira/gtorrent/internal/bitfield"
	"github.com/danferreira/gtorrent/internal/metadata"
)

type Scheduler struct {
	mu       sync.Mutex
	bitfield *bitfield.Bitfield
	pending  map[int]*PieceWork
	keys     []int
}

func NewScheduler(m *metadata.Metadata, bitfield *bitfield.Bitfield) (*Scheduler, error) {
	pieceHashes := m.Info.Pieces
	pieceLength := m.Info.PieceLength
	torrentSize := m.Info.TotalLength()
	pending := make(map[int]*PieceWork)
	var keys []int

	for index, ph := range pieceHashes {
		if bitfield.HasPiece(index) {
			continue
		}

		begin := int64(index * pieceLength)
		end := begin + int64(pieceLength)

		if end > torrentSize {
			end = torrentSize
		}

		actualPieceLength := end - begin

		pending[index] = &PieceWork{Index: index, Hash: ph, Length: int(actualPieceLength)}
		keys = append(keys, index)
	}

	return &Scheduler{
		bitfield: bitfield,
		pending:  pending,
		keys:     keys,
	}, nil
}

func (s *Scheduler) next() *PieceWork {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.pending) == 0 {
		return nil
	}

	idx := s.keys[0]
	pw := s.pending[idx]

	s.keys = s.keys[1:]

	delete(s.pending, idx)

	return pw
}

func (s *Scheduler) Run(ctx context.Context) (<-chan *PieceWork, chan<- *PieceWork) {
	workChan := make(chan *PieceWork, 64)
	failChan := make(chan *PieceWork, 64)

	go func() {
		defer close(workChan)

		for {
			pw := s.next()
			if pw == nil {
				// all done; wait until ctx is canceled or new failures arrive
				select {
				case <-ctx.Done():
					return
				case failed := <-failChan:
					s.requeue(failed)
				}
				continue
			}

			select {
			case workChan <- pw:
			case failed := <-failChan:
				s.requeue(failed)
			case <-ctx.Done():
				return
			}
		}
	}()

	return workChan, failChan
}

func (s *Scheduler) requeue(pw *PieceWork) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.pending[pw.Index]; !ok {
		s.pending[pw.Index] = pw
		s.keys = append(s.keys, pw.Index)
	}
}
