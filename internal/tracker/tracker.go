package tracker

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/danferreira/gtorrent/internal/metadata"
	"github.com/danferreira/gtorrent/internal/peer"
	"github.com/jackpal/bencode-go"
)

type Event string

const (
	EventStarted   Event = "started"
	EventCompleted Event = "completed"
	EventStopped   Event = "stopped"
	EventUpdated   Event = ""
)

type rawResponse struct {
	Interval int
	Peers    string
}

type Tracker struct {
	Metadata metadata.Metadata
	PeerID   [20]byte
}

func (t *Tracker) Announce(e Event, downloaded, uploaded, left int64) ([]peer.Peer, int, error) {
	params := url.Values{
		"info_hash":  []string{string(t.Metadata.Info.InfoHash[:])},
		"peer_id":    []string{string(t.PeerID[:])},
		"port":       []string{strconv.Itoa(6881)},
		"downloaded": []string{strconv.FormatInt(downloaded, 10)},
		"uploaded":   []string{strconv.FormatInt(uploaded, 10)},
		"left":       []string{strconv.FormatInt(left, 10)},
		"compact":    []string{"1"},
	}

	if e != EventUpdated {
		params.Add("event", string(e))
	}

	url := t.Metadata.Announce
	url.RawQuery = params.Encode()
	myDialer := net.Dialer{}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = func(ctx context.Context, _, addr string) (net.Conn, error) {
		return myDialer.DialContext(ctx, "tcp4", addr)
	}
	c := &http.Client{Timeout: 15 * time.Second, Transport: transport}
	resp, err := c.Get(url.String())
	if err != nil {
		return nil, 0, err
	}

	defer resp.Body.Close()

	trackerResp := rawResponse{}
	err = bencode.Unmarshal(resp.Body, &trackerResp)
	if err != nil {
		return nil, 0, err
	}

	peersResp := []byte(trackerResp.Peers)
	chunks := slices.Collect(slices.Chunk(peersResp, 6))

	discoveredPeers := make([]peer.Peer, 0, len(chunks))

	for _, p := range chunks {
		peerv, err := peer.Unmarshal(p)
		if err != nil {
			continue
		}
		discoveredPeers = append(discoveredPeers, peerv)
	}

	return discoveredPeers, trackerResp.Interval, nil
}
