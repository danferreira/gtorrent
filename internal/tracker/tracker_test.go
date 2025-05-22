package tracker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/danferreira/gtorrent/internal/metadata"
	"github.com/danferreira/gtorrent/internal/peer"
	"github.com/jackpal/bencode-go"
	"github.com/stretchr/testify/assert"
)

type fakeTrackerResp struct {
	Interval int    `bencode:"interval"`
	Peers    string `bencode:"peers"`
}

func TestTrackerAnnounce(t *testing.T) {
	fakeResp := fakeTrackerResp{
		Interval: 1800,
		Peers: string([]byte{
			127, 0, 0, 1, 0x1A, 0xE1, // 127.0.0.1:6881
			192, 168, 0, 10, 0x1B, 0x39, // 192.168.0.10:6969
		}),
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("info_hash") == "" {
			t.Error("info_hash was not set in request")
		}

		if err := bencode.Marshal(w, fakeResp); err != nil {
			t.Fatalf("failed to write bencoded response: %v", err)
		}
	}))
	defer server.Close()

	serverUrl, _ := url.Parse(server.URL)

	testMetadata := &metadata.Metadata{
		Announce: serverUrl,
		Info: metadata.Info{
			InfoHash: [20]byte{1, 2, 3, 4},
		},
	}

	peerID := [20]byte{1, 2, 3, 4}
	tr := NewTracker(testMetadata, peerID, 6881)

	gotPeers, interval, err := tr.Announce(context.Background(), EventStarted, 0, 0, 0)

	assert.Nil(t, err)
	assert.Equal(t, 1800, interval)
	assert.Equal(t, 2, len(gotPeers))
	assert.Equal(t, []peer.Peer{{
		Addr: "127.0.0.1:6881",
	}, {
		Addr: "192.168.0.10:6969",
	}}, gotPeers)
}
