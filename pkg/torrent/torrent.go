package torrent

import (
	"bytes"
	"crypto/sha1"
	"fmt"
	"math"
	"net"
	"sync"
	"time"

	"github.com/danferreira/gtorrent/pkg/metadata"
	"github.com/danferreira/gtorrent/pkg/peers"
	"github.com/danferreira/gtorrent/pkg/pieces"
	"github.com/danferreira/gtorrent/pkg/storage"
	"github.com/danferreira/gtorrent/pkg/tracker"
)

type PeerEventType int

const (
	EventNewPeer PeerEventType = iota
	EventPeerDisconnected
	EventPieceDownloaded
)

type PeerEvent struct {
	Type  PeerEventType
	Peer  *peers.Peer
	Piece *pieces.PieceDownloaded
}

type Torrent struct {
	mu             sync.Mutex
	Metadata       metadata.Metadata
	Stats          *pieces.TorrentStats
	PeerID         [20]byte
	ConnectedPeers map[string]struct{}
	Storage        *storage.Storage
}

func (t *Torrent) Run() error {
	pieceHashes := t.Metadata.Info.Pieces
	pieceLength := t.Metadata.Info.PieceLength
	torrentSize := t.Metadata.Info.TotalLength()

	bitfieldSize := int(math.Ceil(float64(len(pieceHashes)) / 8))
	bitfield := make(pieces.Bitfield, bitfieldSize)

	resultChan := make(chan pieces.PieceDownloaded)
	doneChan := make(chan bool)

	storage, err := storage.NewStorage(t.Metadata.Info.Files)
	if err != nil {
		return err
	}
	t.Storage = storage

	var piecesLeft []pieces.PieceWork
	t.Stats = &pieces.TorrentStats{
		Downloaded: 0,
		Uploaded:   0,
		Left:       int64(torrentSize),
	}

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
			bitfield.SetPiece(index)
			amount := int64(len(data))
			t.Stats.Downloaded += amount
			t.Stats.Left -= amount
			continue
		}

		piecesLeft = append(piecesLeft, pieces.PieceWork{Index: index, Hash: ph, Length: actualPieceLength})
	}

	if len(piecesLeft) == 0 {
		fmt.Println("Nothing to download. Seeding mode")
	}
	t.ConnectedPeers = make(map[string]struct{})

	peersChan := make(chan []peers.Peer)
	announceChan := make(chan tracker.Event)
	piecesChan := make(chan *pieces.PieceWork, len(pieceHashes))

	for _, ph := range piecesLeft {
		piecesChan <- &ph
	}

	// sigChan := make(chan os.Signal, 1)
	// signal.Notify(sigChan, os.Interrupt)

	if err := t.ListenForPeers(bitfield, resultChan, piecesChan, doneChan); err != nil {
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
			t.Stats.UpdateDownloaded(int64(len(pd.Data)))
			downloaded, _, left := t.Stats.GetSnapshot()

			percent := (float64(downloaded) / float64(torrentSize)) * 100
			fmt.Printf("Downloading:  %.2f%% of %d from %d peers\n", percent, torrentSize, len(t.ConnectedPeers))

			if left == 0 {
				_ = t.Storage.CloseFiles()
				announceChan <- tracker.EventCompleted
			}
		case ps := <-peersChan:
			if len(piecesLeft) > 0 {
				fmt.Println("Connecting to peers")
				for _, peer := range ps {
					peerAddr := peer.Addr
					fmt.Println("Peer Addr", peerAddr)
					if peerAddr == "127.0.0.1:6881" {
						fmt.Printf("Own address. Skipping...")
						continue
					}

					if _, connected := t.ConnectedPeers[peerAddr]; connected {
						fmt.Printf("Peer %s is already connected\n", peerAddr)
						continue
					}

					t.ConnectedPeers[peerAddr] = struct{}{}
					go t.newPeerWorker(peer, resultChan, piecesChan, doneChan)
				}
			}
		}
	}
}

func (t *Torrent) ListenForPeers(bitfield pieces.Bitfield, resultChan chan pieces.PieceDownloaded, piecesChan chan *pieces.PieceWork, done chan bool) error {
	fmt.Println("Listen for connections")
	ln, err := net.Listen("tcp4", ":6881")
	if err != nil {
		fmt.Println("Error listening for connections: ", err)
		return err
	}

	go func(ln net.Listener) {
		for {
			conn, err := ln.Accept()
			if err != nil {
				fmt.Println("Error during accepting new conn: ", err)
				continue
			}
			go t.newPeerConn(conn, bitfield, resultChan, piecesChan, done)
		}
	}(ln)

	return nil
}

func (t *Torrent) newPeerConn(conn net.Conn, bitfield pieces.Bitfield, resultChan chan pieces.PieceDownloaded, piecesChan chan *pieces.PieceWork, doneChan chan bool) {
	fmt.Println("New peer connection:", conn.RemoteAddr().String())
	peer := peers.Peer{
		Addr: conn.RemoteAddr().String(),
	}

	pc := &peers.PeerConnection{
		Peer:        &peer,
		InfoHash:    t.Metadata.Info.InfoHash,
		PeerID:      t.PeerID,
		ResultChan:  resultChan,
		PiecesChan:  piecesChan,
		OwnBitfield: &bitfield,
		Storage:     t.Storage,
		PieceLength: t.Metadata.Info.PieceLength,
	}

	err := pc.AcceptConnection(conn)
	if err != nil {
		fmt.Println("Error when accepting connection", err)
	}

	err = pc.ExchangeMessages(doneChan)
	if err != nil {
		fmt.Println("Error when exchanging messages with peer", err)
	}

}

func (t *Torrent) newPeerWorker(peer peers.Peer, resultChan chan pieces.PieceDownloaded, piecesChan chan *pieces.PieceWork, doneChan chan bool) {
	pc := &peers.PeerConnection{
		Peer:        &peer,
		InfoHash:    t.Metadata.Info.InfoHash,
		PeerID:      t.PeerID,
		ResultChan:  resultChan,
		PiecesChan:  piecesChan,
		Storage:     t.Storage,
		PieceLength: t.Metadata.Info.PieceLength,
	}

	if err := pc.Connect(doneChan); err != nil {
		fmt.Println("Error during connection: ", err)

		fmt.Printf("Removing %s from list of connected peers\n", peer.Addr)
		t.mu.Lock()
		defer t.mu.Unlock()
		delete(t.ConnectedPeers, peer.Addr)

		return
	}

	err := pc.ExchangeMessages(doneChan)
	if err != nil {
		fmt.Println("Error when exchanging messages with peer", err)
	}
}

func (t *Torrent) announceWorker(peersChan chan []peers.Peer, announceChan chan tracker.Event) {
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

func (t *Torrent) checkIntegrity(expectedHash [20]byte, data []byte) bool {
	hash := sha1.Sum(data)
	return bytes.Equal(hash[:], expectedHash[:])
}
