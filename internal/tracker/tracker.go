package tracker

import (
	"context"
	"fmt"
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
	metadata *metadata.Metadata
	peerID   [20]byte
	port     int
	client   *http.Client
}

func NewTracker(m *metadata.Metadata, peerID [20]byte, listenPort int) *Tracker {
	tr := &Tracker{metadata: m, peerID: peerID, port: listenPort}
	myDialer := net.Dialer{}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = func(ctx context.Context, _, addr string) (net.Conn, error) {
		return myDialer.DialContext(ctx, "tcp4", addr)
	}

	tr.client = &http.Client{Timeout: 15 * time.Second, Transport: transport}

	return tr
}

func (t *Tracker) Announce(ctx context.Context, e Event, downloaded, uploaded, left int64) ([]peer.Peer, int, error) {
	params := url.Values{
		"info_hash":  []string{url.QueryEscape(string(t.metadata.Info.InfoHash[:]))},
		"peer_id":    []string{url.QueryEscape(string(t.peerID[:]))},
		"port":       []string{strconv.Itoa(t.port)},
		"downloaded": []string{strconv.FormatInt(downloaded, 10)},
		"uploaded":   []string{strconv.FormatInt(uploaded, 10)},
		"left":       []string{strconv.FormatInt(left, 10)},
		"compact":    []string{"1"},
	}

	if e != EventUpdated {
		params.Add("event", string(e))
	}

	url := t.metadata.Announce
	url.RawQuery = params.Encode()

	r, err := http.NewRequestWithContext(ctx, http.MethodGet, url.String(), nil)
	if err != nil {
		return nil, 0, err
	}

	resp, err := t.client.Do(r)
	if err != nil {
		return nil, 0, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("tracker HTTP %d", resp.StatusCode)
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
