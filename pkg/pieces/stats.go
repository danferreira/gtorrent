package pieces

import (
	"sync"
)

type TorrentStats struct {
	mu         sync.Mutex
	Downloaded int64
	Uploaded   int64
	Left       int64
}

func (s *TorrentStats) GetSnapshot() (downloaded, uploaded, left int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Downloaded, s.Uploaded, s.Left
}

func (s *TorrentStats) UpdateDownloaded(amount int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Downloaded += amount
	s.Left -= amount
}

func (s *TorrentStats) UpdateUploaded(amount int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Uploaded += amount
}
