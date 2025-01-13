package torrent

import (
	"fmt"
	"time"

	"github.com/danferreira/gtorrent/pkg/metadata"
	"github.com/danferreira/gtorrent/pkg/peers"
	"github.com/danferreira/gtorrent/pkg/tracker"
)

type PeerEventType int

const (
	EventNewPeer PeerEventType = iota
	EventPeerDisconnected
)

type PeerEvent struct {
	Type PeerEventType
	Peer *peers.Peer
}

type Torrent struct {
	Metadata       metadata.Metadata
	PeerID         [20]byte
	ConnectedPeers map[string]struct{}
}

func (t *Torrent) Download() error {
	t.ConnectedPeers = make(map[string]struct{})

	eventsCh := make(chan *PeerEvent)

	go t.announceWorker(eventsCh)

	for event := range eventsCh {
		switch event.Type {
		case EventNewPeer:
			if event.Peer == nil {
				continue
			}

			peerAddr := event.Peer.Addr()
			if _, connected := t.ConnectedPeers[peerAddr]; connected {
				fmt.Printf("Peer %s is already connected\n", peerAddr)
				continue
			}

			go t.newPeerWorker(*event.Peer, eventsCh)
		case EventPeerDisconnected:
			if event.Peer == nil {
				continue
			}
			fmt.Printf("Removing %s from list of connected peers\n", event.Peer.Addr())
			delete(t.ConnectedPeers, event.Peer.Addr())
		}
	}
	return nil
}

func (t *Torrent) newPeerWorker(peer peers.Peer, eventsCh chan *PeerEvent) {
	pc := &peers.PeerConnection{
		Peer:     &peer,
		InfoHash: t.Metadata.Info.InfoHash,
		PeerID:   t.PeerID,
	}
	peerAddr := peer.Addr()

	t.ConnectedPeers[peerAddr] = struct{}{}

	if err := pc.Connect(); err != nil {
		fmt.Println("Error during connection: ", err)
		eventsCh <- &PeerEvent{
			Type: EventPeerDisconnected,
			Peer: pc.Peer,
		}
	}
}

func (t *Torrent) announceWorker(eventsCh chan *PeerEvent) error {
	fmt.Println("Starting announce worker")

	tkr := tracker.Tracker{
		Metadata: t.Metadata,
		PeerID:   t.PeerID,
	}

	for {
		fmt.Println("Announcing to tracker")
		received_peers, interval, err := tkr.Announce(tracker.Started)
		if err != nil {
			fmt.Println("An error occurred while trying to call tracker. Retrying in 10s.\nError: ", err)
			time.Sleep(time.Duration(10) * time.Second)
			continue
		}

		for _, rc_p := range received_peers {
			eventsCh <- &PeerEvent{
				Type: EventNewPeer,
				Peer: &rc_p,
			}
		}

		time.Sleep(time.Duration(interval) * time.Second)
	}
}
