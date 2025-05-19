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
}

func (t *Torrent) Run(ctx context.Context) error {
	pieceHashes := t.Metadata.Info.Pieces
	pieceLength := t.Metadata.Info.PieceLength
	torrentSize := t.Metadata.Info.TotalLength()

	storage, err := storage.NewStorage(t.Metadata.Info.Files)
	if err != nil {
		return err
	}
	t.Storage = storage

	t.Stats = &piece.TorrentStats{
		Downloaded: 0,
		Uploaded:   0,
		Left:       int64(torrentSize),
		Size:       int64(torrentSize),
	}
	bitfieldSize := int(math.Ceil(float64(len(pieceHashes)) / 8))
	bitfield := make(piece.Bitfield, bitfieldSize)
	t.Bitfield = &bitfield

	piecesChan := make(chan *piece.PieceWork, len(pieceHashes))
	t.checkLocalFiles(piecesChan)
	if len(piecesChan) == 0 {
		slog.Info("Nothing to download. Seeding mode")
		t.SeedingOnly = true
	}

	t.connectedPeers = make(map[string]*peer.Connection)

	peersChan := make(chan []peer.Peer)
	announceChan := make(chan tracker.Event)

	resultChan := make(chan piece.PieceDownloaded)
	doneChan := make(chan bool)

	if err := t.ListenForPeers(resultChan, piecesChan, doneChan); err != nil {
		return err
	}

	go t.announceWorker(peersChan, announceChan)

	for {
		select {
		case pd := <-resultChan:
			start := pd.Index * pieceLength
			if err := t.Storage.Write(start, pd.Data); err != nil {
				slog.Error("Error writing piece to disk", "error", err)
				piecesChan <- &pd.PW
				continue
			}

			t.Bitfield.SetPiece(pd.Index)
			t.broadcastHavePiece(pd.Index)

			t.Stats.UpdateDownloaded(int64(len(pd.Data)))
			_, _, left, _ := t.Stats.GetSnapshot()
			t.StatsChan <- *t.Stats

			if left == 0 {
				_ = t.Storage.CloseFiles()
				announceChan <- tracker.EventCompleted
				t.SeedingOnly = true
				close(doneChan)
			}
		case ps := <-peersChan:
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

					go t.newPeerWorker(peer, resultChan, piecesChan, doneChan)
				}
			}
		case <-ctx.Done():
			return nil
		}
	}
}

func (t *Torrent) ListenForPeers(resultChan chan piece.PieceDownloaded, piecesChan chan *piece.PieceWork, done chan bool) error {
	slog.Info("Listen for connections")
	ln, err := net.Listen("tcp4", ":6881")
	if err != nil {
		return err
	}

	go func(ln net.Listener) {
		for {
			conn, err := ln.Accept()
			if err != nil {
				slog.Error("Error during accepting new conn", "error", err)
				continue
			}
			go t.newPeerConn(conn, resultChan, piecesChan, done)
		}
	}(ln)

	return nil
}

func (t *Torrent) newPeerConn(conn net.Conn, resultChan chan piece.PieceDownloaded, piecesChan chan *piece.PieceWork, doneChan chan bool) {
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

	err = pc.ExchangeMessages(piecesChan, resultChan, doneChan)
	if err != nil {
		slog.Error("Error when exchanging messages with peer", "error", err)
	}

}

func (t *Torrent) newPeerWorker(p peer.Peer, resultChan chan piece.PieceDownloaded, piecesChan chan *piece.PieceWork, doneChan chan bool) {
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

	err := pc.ExchangeMessages(piecesChan, resultChan, doneChan)
	if err != nil {
		slog.Error("Error when exchanging messages with peer", "error", err)
		t.removeConnectedPeer(&p)
	}
}

func (t *Torrent) announceWorker(peersChan chan []peer.Peer, announceChan chan tracker.Event) {
	slog.Info("Starting announce worker")

	tkr := tracker.Tracker{
		Metadata: t.Metadata,
		PeerID:   t.PeerID,
	}

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

		peersChan <- receivedPeers
	}

	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case event := <-announceChan:
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

				peersChan <- receivedPeers

			}
		}
	}
}

func (t *Torrent) checkLocalFiles(piecesChan chan<- *piece.PieceWork) {
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

		piecesChan <- &piece.PieceWork{Index: index, Hash: ph, Length: actualPieceLength}
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
