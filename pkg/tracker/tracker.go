package tracker

import (
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/danferreira/gtorrent/pkg/metadata"
	peer "github.com/danferreira/gtorrent/pkg/peers"
	"github.com/jackpal/bencode-go"
)

type TrackerEvent uint8

const (
	Started TrackerEvent = iota
	Stopped
	Completed
)

func (e TrackerEvent) String() string {
	switch e {
	case Started:
		return "started"
	case Stopped:
		return "stopped"
	case Completed:
		return "completed"
	}

	return ""
}

type TrackerResp struct {
	Interval int
	Peers    string
}

type Tracker struct {
	Metadata metadata.Metadata
	PeerID   [20]byte
}

type TrackerResponse struct {
	Peers    []peer.Peer
	Interval uint
}

func (t *Tracker) Announce(e TrackerEvent) ([]peer.Peer, int, error) {
	params := url.Values{
		"info_hash":  []string{string(t.Metadata.Info.InfoHash[:])},
		"peer_id":    []string{string(t.PeerID[:])},
		"port":       []string{strconv.Itoa(6881)},
		"uploaded":   []string{"0"},
		"downloaded": []string{"0"},
		"compact":    []string{"1"},
		"left":       []string{strconv.Itoa(t.Metadata.Info.TotalLength())},
		"event":      []string{e.String()},
	}
	url := t.Metadata.Announce
	url.RawQuery = params.Encode()

	c := &http.Client{Timeout: 15 * time.Second}
	resp, err := c.Get(url.String())
	if err != nil {
		return nil, 0, err
	}

	defer resp.Body.Close()

	trackerResp := TrackerResp{}
	err = bencode.Unmarshal(resp.Body, &trackerResp)
	if err != nil {
		return nil, 0, err
	}

	peersResp := []byte(trackerResp.Peers)
	chunks := slices.Collect(slices.Chunk(peersResp, 6))

	peers := make([]peer.Peer, 0, len(chunks))

	for _, p := range chunks {
		peerv, err := peer.Unmarshal(p)
		if err != nil {
			continue
		}
		peers = append(peers, peerv)
	}

	return peers, trackerResp.Interval, nil
}
