package torrent

import (
	"bytes"
	"crypto/sha1"
	"fmt"
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

type PieceDownloaded struct {
	Index int
	Data  []byte
}

type PeerEvent struct {
	Type  PeerEventType
	Peer  *peers.Peer
	Piece *PieceDownloaded
}

type Torrent struct {
	Metadata       metadata.Metadata
	Stats          *pieces.TorrentStats
	PeerID         [20]byte
	ConnectedPeers map[string]struct{}
	Storage        *storage.Storage
}

func (t *Torrent) Download() error {
	pieceHashes := t.Metadata.Info.Pieces
	pieceLength := t.Metadata.Info.PieceLength
	torrentSize := t.Metadata.Info.TotalLength()
	piecesChan := make(chan *pieces.PieceWork, len(pieceHashes))
	storage, err := storage.NewStorage(t.Metadata.Info.Files)
	if err != nil {
		return err
	}
	t.Storage = storage

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
			continue
		}

		piecesChan <- &pieces.PieceWork{Index: index, Hash: ph, Length: actualPieceLength}
	}

	t.ConnectedPeers = make(map[string]struct{})

	peersChan := make(chan []peers.Peer)
	peerEventChan := make(chan PeerEvent)
	announceChan := make(chan tracker.Event)
	doneChan := make(chan bool)
	// sigChan := make(chan os.Signal, 1)
	// signal.Notify(sigChan, os.Interrupt)

	go t.announceWorker(peersChan, announceChan)

	piecesDownloaded := 0

	for {
		select {
		case ps := <-peersChan:
			for _, peer := range ps {
				peerAddr := peer.Addr()
				if _, connected := t.ConnectedPeers[peerAddr]; connected {
					fmt.Printf("Peer %s is already connected\n", peerAddr)
					continue
				}

				go t.newPeerWorker(peer, peerEventChan, piecesChan, doneChan)
			}
		case event := <-peerEventChan:
			switch event.Type {
			case EventPieceDownloaded:
				if event.Piece == nil {
					continue
				}

				start := event.Piece.Index * pieceLength
				t.Storage.Write(start, event.Piece.Data)
				t.Stats.UpdateDownloaded(int64(len(event.Piece.Data)))

				piecesDownloaded++
				if piecesDownloaded == len(pieceHashes) {
					t.Storage.CloseFiles()
					announceChan <- tracker.EventCompleted
				}

			case EventPeerDisconnected:
				if event.Peer == nil {
					continue
				}
				fmt.Printf("Removing %s from list of connected peers\n", event.Peer.Addr())
				delete(t.ConnectedPeers, event.Peer.Addr())
			}
			// case <-sigChan:
			// 	fmt.Println("Quitting...")
			// 	announceChan <- tracker.EventStopped

		}
	}
}

func (t *Torrent) newPeerWorker(peer peers.Peer, eventsCh chan PeerEvent, piecesChan chan *pieces.PieceWork, doneChan chan bool) {
	pc := &peers.PeerConnection{
		Peer:     &peer,
		InfoHash: t.Metadata.Info.InfoHash,
		PeerID:   t.PeerID,
	}
	pieces := len(t.Metadata.Info.Pieces)
	peerAddr := peer.Addr()

	t.ConnectedPeers[peerAddr] = struct{}{}

	if err := pc.Connect(doneChan); err != nil {
		fmt.Println("Error during connection: ", err)
		eventsCh <- PeerEvent{
			Type: EventPeerDisconnected,
			Peer: pc.Peer,
		}
		return
	}

	for pw := range piecesChan {
		data, err := pc.AttemptDownloadPiece(pw, doneChan)
		if err != nil {
			fmt.Println("An error occurred", err)
			piecesChan <- pw
			continue
		}

		if !t.checkIntegrity(pw.Hash, data) {
			fmt.Println("Invalid piece", pw.Index)
			piecesChan <- pw
			continue
		}

		fmt.Printf("Piece %d/%d downloaded by peer %s\n", pw.Index+1, pieces, peer.Addr())
		eventsCh <- PeerEvent{
			Type: EventPieceDownloaded,
			Piece: &PieceDownloaded{
				Index: pw.Index,
				Data:  data,
			},
		}

	}
}

func (t *Torrent) checkIntegrity(expectedHash [20]byte, data []byte) bool {
	hash := sha1.Sum(data)
	return bytes.Equal(hash[:], expectedHash[:])
}

func (t *Torrent) announceWorker(peersChan chan []peers.Peer, announceChan chan tracker.Event) error {
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
