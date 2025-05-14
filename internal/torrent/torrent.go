package torrent

import (
	"bytes"
	"crypto/sha1"
	"fmt"
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
}

func (t *Torrent) Run() error {
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
	}
	bitfieldSize := int(math.Ceil(float64(len(pieceHashes)) / 8))
	bitfield := make(piece.Bitfield, bitfieldSize)
	t.Bitfield = &bitfield

	piecesChan := make(chan *piece.PieceWork, len(pieceHashes))
	t.checkLocalFiles(piecesChan)
	if len(piecesChan) == 0 {
		fmt.Println("Nothing to download. Seeding mode")
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
				fmt.Println("Error writing piece to disk: ", err)
				piecesChan <- &pd.PW
				continue
			}

			t.Bitfield.SetPiece(pd.Index)
			t.broadcastHavePiece(pd.Index)

			t.Stats.UpdateDownloaded(int64(len(pd.Data)))
			downloaded, _, left := t.Stats.GetSnapshot()

			percent := (float64(downloaded) / float64(torrentSize)) * 100
			fmt.Printf("Downloading:  %.2f%% of %d from %d peers\n", percent, torrentSize, len(t.connectedPeers))

			if left == 0 {
				_ = t.Storage.CloseFiles()
				announceChan <- tracker.EventCompleted
				t.SeedingOnly = true
				close(doneChan)
			}
		case ps := <-peersChan:
			if !t.SeedingOnly {
				fmt.Println("Connecting to peers")
				for _, peer := range ps {
					peerAddr := peer.Addr
					fmt.Println("Peer Addr", peerAddr)
					if peerAddr == "127.0.0.1:6881" {
						fmt.Printf("Own address. Skipping...")
						continue
					}

					if _, connected := t.connectedPeers[peerAddr]; connected {
						fmt.Printf("Peer %s is already connected\n", peerAddr)
						continue
					}

					go t.newPeerWorker(peer, resultChan, piecesChan, doneChan)
				}
			}
		}
	}
}

func (t *Torrent) ListenForPeers(resultChan chan piece.PieceDownloaded, piecesChan chan *piece.PieceWork, done chan bool) error {
	fmt.Println("Listen for connections")
	ln, err := net.Listen("tcp4", ":6881")
	if err != nil {
		return err
	}

	go func(ln net.Listener) {
		for {
			conn, err := ln.Accept()
			if err != nil {
				fmt.Println("Error during accepting new conn: ", err)
				continue
			}
			go t.newPeerConn(conn, resultChan, piecesChan, done)
		}
	}(ln)

	return nil
}

func (t *Torrent) newPeerConn(conn net.Conn, resultChan chan piece.PieceDownloaded, piecesChan chan *piece.PieceWork, doneChan chan bool) {
	fmt.Println("New peer connection:", conn.RemoteAddr().String())
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
		fmt.Println("Error when accepting connection", err)
	}

	err = pc.ExchangeMessages(piecesChan, resultChan, doneChan)
	if err != nil {
		fmt.Println("Error when exchanging messages with peer", err)
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
		fmt.Println("Error during connection: ", err)

		fmt.Printf("Removing %s from list of connected peers\n", p.Addr)
		return
	}

	t.addConnectedPeer(pc)

	err := pc.ExchangeMessages(piecesChan, resultChan, doneChan)
	if err != nil {
		fmt.Println("Error when exchanging messages with peer", err)
		t.removeConnectedPeer(&p)
	}
}

func (t *Torrent) announceWorker(peersChan chan []peer.Peer, announceChan chan tracker.Event) {
	fmt.Println("Starting announce worker")

	tkr := tracker.Tracker{
		Metadata: t.Metadata,
		PeerID:   t.PeerID,
	}

	currentEvent := tracker.EventStarted
	downloaded, uploaded, left := t.Stats.GetSnapshot()
	receivedPeers, interval, err := tkr.Announce(tracker.EventStarted, downloaded, uploaded, left)
	if err != nil {
		fmt.Println("An error occurred while trying to call tracker. Retrying in 10s.\nError: ", err)
		interval = 10
	} else {
		fmt.Println("Successfully announced to tracker")
		currentEvent = tracker.EventUpdated

		peersChan <- receivedPeers
	}

	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case event := <-announceChan:
			fmt.Printf("Send %s event to tracker\n", event)
			downloaded, uploaded, left := t.Stats.GetSnapshot()
			_, _, _ = tkr.Announce(event, downloaded, uploaded, left)
			fmt.Printf("Event %s sent\n", event)
		case <-ticker.C:
			downloaded, uploaded, left = t.Stats.GetSnapshot()
			receivedPeers, interval, err := tkr.Announce(currentEvent, downloaded, uploaded, left)
			if err != nil {
				fmt.Println("Error on tracker announce:", err)
			} else {
				fmt.Println("Successfully announced to tracker")
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

	fmt.Println("Checking files on disk...")
	for index, ph := range pieceHashes {
		begin := index * pieceLength
		end := begin + pieceLength

		if end > torrentSize {
			end = torrentSize
		}

		actualPieceLength := end - begin

		data, err := storage.Read(begin, actualPieceLength)
		if err != nil {
			fmt.Println("Disk check error: ", err)
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
			fmt.Println("Error sending Have message to peer: ", err)
		}
	}
}
