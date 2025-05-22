package torrent

import (
	"bytes"
	"context"
	"crypto/sha1"
	"log/slog"
	"math"
	"net"
	"sync"
	"time"

	"github.com/danferreira/gtorrent/internal/metadata"
	"github.com/danferreira/gtorrent/internal/peer"
	"github.com/danferreira/gtorrent/internal/piece"
	"github.com/danferreira/gtorrent/internal/storage"
	"github.com/danferreira/gtorrent/internal/tracker"
)

type PeerEventType int

const (
	EventNewPeer PeerEventType = iota
	EventPeerDisconnected
	EventPieceDownloaded
)

type PeerEvent struct {
	Type  PeerEventType
	Peer  *peer.Peer
	Piece *piece.PieceDownloaded
}

type Torrent struct {
	mu             sync.Mutex
	Metadata       metadata.Metadata
	Stats          *piece.TorrentStats
	PeerID         [20]byte
	connectedPeers map[string]*peer.Connection
	Storage        *storage.Storage
	SeedingOnly    bool
	Bitfield       *piece.Bitfield
	StatsChan      chan<- piece.TorrentStats

	peersChan    chan []peer.Peer
	piecesChan   chan *piece.PieceWork
	resultChan   chan piece.PieceDownloaded
	announceChan chan tracker.Event
	doneChan     chan bool
}

func NewTorrent(m *metadata.Metadata, peerID [20]byte) (*Torrent, error) {
	storage, err := storage.NewStorage(m.Info.Files)
	if err != nil {
		return nil, err
	}

	torrentSize := m.Info.TotalLength()
	stats := &piece.TorrentStats{
		Downloaded: 0,
		Uploaded:   0,
		Left:       int64(torrentSize),
		Size:       int64(torrentSize),
	}

	pieceHashes := m.Info.Pieces
	bitfieldSize := int(math.Ceil(float64(len(pieceHashes)) / 8))
	bitfield := make(piece.Bitfield, bitfieldSize)

	piecesChan := make(chan *piece.PieceWork, len(pieceHashes))

	connectedPeers := make(map[string]*peer.Connection)

	peersChan := make(chan []peer.Peer)
	announceChan := make(chan tracker.Event)

	resultChan := make(chan piece.PieceDownloaded)
	doneChan := make(chan bool)

	return &Torrent{
		Metadata:       *m,
		PeerID:         peerID,
		Storage:        storage,
		Stats:          stats,
		Bitfield:       &bitfield,
		piecesChan:     piecesChan,
		connectedPeers: connectedPeers,
		peersChan:      peersChan,
		announceChan:   announceChan,
		resultChan:     resultChan,
		doneChan:       doneChan,
	}, nil
}

func (t *Torrent) Start(ctx context.Context) error {
	pieceLength := t.Metadata.Info.PieceLength

	t.checkLocalFiles()
	if len(t.piecesChan) == 0 {
		slog.Info("Nothing to download. Seeding mode")
		t.SeedingOnly = true
	}

	go t.announceWorker()

	for {
		select {
		case pd := <-t.resultChan:
			start := pd.Index * pieceLength
			if err := t.Storage.Write(start, pd.Data); err != nil {
				slog.Error("Error writing piece to disk", "error", err)
				t.piecesChan <- &pd.PW
				continue
			}

			t.Bitfield.SetPiece(pd.Index)
			t.broadcastHavePiece(pd.Index)

			t.Stats.UpdateDownloaded(int64(len(pd.Data)))
			_, _, left, _ := t.Stats.GetSnapshot()
			t.StatsChan <- *t.Stats

			if left == 0 {
				_ = t.Storage.CloseFiles()
				t.announceChan <- tracker.EventCompleted
				t.SeedingOnly = true
				close(t.doneChan)
			}
		case ps := <-t.peersChan:
			if !t.SeedingOnly {
				slog.Info("Connecting to peers")
				for _, peer := range ps {
					peerAddr := peer.Addr
					if peerAddr == "127.0.0.1:6881" {
						slog.Info("Own address. Skipping...")
						continue
					}

					if _, connected := t.connectedPeers[peerAddr]; connected {
						slog.Info("Peer is already connected", "peer", peerAddr)
						continue
					}

					go t.newPeerWorker(peer)
				}
			}
		case <-ctx.Done():
			slog.Info("Context done")
			return nil
		}
	}
}

func (t *Torrent) NewPeerConn(conn net.Conn) {
	slog.Info("New peer connection", "peer", conn.RemoteAddr().String())
	p := peer.Peer{
		Addr: conn.RemoteAddr().String(),
	}

	pc := &peer.Connection{
		Peer:        &p,
		InfoHash:    t.Metadata.Info.InfoHash,
		PeerID:      t.PeerID,
		OwnBitfield: t.Bitfield,
		Storage:     t.Storage,
		PieceLength: t.Metadata.Info.PieceLength,
	}

	err := pc.AcceptConnection(conn)
	if err != nil {
		slog.Error("Error when accepting connection", "error", err)
	}

	err = pc.ExchangeMessages(t.piecesChan, t.resultChan, t.doneChan)
	if err != nil {
		slog.Error("Error when exchanging messages with peer", "error", err)
	}

}

func (t *Torrent) newPeerWorker(p peer.Peer) {
	pc := &peer.Connection{
		Peer:        &p,
		InfoHash:    t.Metadata.Info.InfoHash,
		PeerID:      t.PeerID,
		Storage:     t.Storage,
		PieceLength: t.Metadata.Info.PieceLength,
	}

	if err := pc.Connect(); err != nil {
		slog.Error("Error during connection", "error", err)
		slog.Info("Removing from list of connected peers", "peer", p.Addr)
		return
	}

	t.addConnectedPeer(pc)

	err := pc.ExchangeMessages(t.piecesChan, t.resultChan, t.doneChan)
	if err != nil {
		slog.Error("Error when exchanging messages with peer", "error", err)
		t.removeConnectedPeer(&p)
	}
}

func (t *Torrent) announceWorker() {
	slog.Info("Starting announce worker")

	tkr := tracker.NewTracker(&t.Metadata, t.PeerID)

	currentEvent := tracker.EventStarted
	downloaded, uploaded, left, _ := t.Stats.GetSnapshot()
	receivedPeers, interval, err := tkr.Announce(tracker.EventStarted, downloaded, uploaded, left)
	if err != nil {
		slog.Warn("An error occurred while trying to call tracker.", "error", err)
		slog.Info("Retrying in 10s.")
		interval = 10
	} else {
		slog.Info("Successfully announced to tracker")
		currentEvent = tracker.EventUpdated

		t.peersChan <- receivedPeers
	}

	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case event := <-t.announceChan:
			slog.Debug("Sending event to tracker", "event", event)
			downloaded, uploaded, left, _ := t.Stats.GetSnapshot()
			_, _, _ = tkr.Announce(event, downloaded, uploaded, left)
			slog.Debug("Event sent", "event", event)
		case <-ticker.C:
			downloaded, uploaded, left, _ = t.Stats.GetSnapshot()
			receivedPeers, interval, err := tkr.Announce(currentEvent, downloaded, uploaded, left)
			if err != nil {
				slog.Error("Error on tracker announce", "error", err)
			} else {
				slog.Info("Successfully announced to tracker")
				currentEvent = tracker.EventUpdated
				ticker.Reset(time.Duration(interval) * time.Second)

				t.peersChan <- receivedPeers
			}
		}
	}
}

func (t *Torrent) checkLocalFiles() {
	pieceHashes := t.Metadata.Info.Pieces
	pieceLength := t.Metadata.Info.PieceLength
	torrentSize := t.Metadata.Info.TotalLength()
	storage := t.Storage

	slog.Info("Checking files on disk...")
	for index, ph := range pieceHashes {
		begin := index * pieceLength
		end := begin + pieceLength

		if end > torrentSize {
			end = torrentSize
		}

		actualPieceLength := end - begin

		data, err := storage.Read(begin, actualPieceLength)
		if err != nil {
			slog.Error("Disk check error", "error", err)
		} else if t.checkIntegrity(ph, data) {
			t.Bitfield.SetPiece(index)
			amount := int64(len(data))
			t.Stats.Downloaded += amount
			t.Stats.Left -= amount
			continue
		}

		t.piecesChan <- &piece.PieceWork{Index: index, Hash: ph, Length: actualPieceLength}
	}
}

func (t *Torrent) checkIntegrity(expectedHash [20]byte, data []byte) bool {
	hash := sha1.Sum(data)
	return bytes.Equal(hash[:], expectedHash[:])
}

func (t *Torrent) addConnectedPeer(pc *peer.Connection) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.connectedPeers[pc.Peer.Addr] = pc
}

func (t *Torrent) removeConnectedPeer(peer *peer.Peer) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.connectedPeers, peer.Addr)
}

func (t *Torrent) broadcastHavePiece(index int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, pc := range t.connectedPeers {
		if err := pc.SendHave(index); err != nil {
			slog.Error("Error sending Have message to peer", "error", err)
		}
	}
}
