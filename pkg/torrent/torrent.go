package torrent

import (
	"fmt"

	"github.com/danferreira/gtorrent/pkg/metadata"
	"github.com/danferreira/gtorrent/pkg/peers"
	"github.com/danferreira/gtorrent/pkg/tracker"
)

type Torrent struct {
	Metadata metadata.Metadata
}

func (t *Torrent) Download() error {
	tkr := tracker.Tracker{
		Metadata: t.Metadata,
		PeerID:   "-GT0001-abcdefgh1234",
	}

	received_peers, err := tkr.Announce(tracker.Started)

	if err != nil {
		return err
	}

	for _, peer := range received_peers {
		var peerID [20]byte
		copy(peerID[:], "-GT0001-abcdefgh1234")
		pc := peers.PeerConnection{
			Peer:     peer,
			InfoHash: t.Metadata.Info.InfoHash,
			PeerID:   peerID,
		}

		err := pc.Connect()

		if err != nil {
			fmt.Println("Error when connecting", err)
			return err
		}

	}

	return nil
}
