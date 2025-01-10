package tracker

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/danferreira/gtorrent/pkg/metadata"
	"github.com/danferreira/gtorrent/pkg/peers"
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
		// "peers" are in “compact” form: 6 bytes (4 for IP, 2 for port) per peer
		// e.g., 127.0.0.1:6881 -> 0x7F 0x00 0x00 0x01 0x1A 0xE1
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

	testMetadata := metadata.Metadata{
		Announce: serverUrl,
		Info: metadata.MetadataInfo{
			InfoHash: [20]byte{1, 2, 3, 4},
		},
	}

	tr := Tracker{
		Metadata: testMetadata,
		PeerID:   "-GT0001-abcdefgh1234",
	}

	gotPeers, err := tr.Announce(Started)

	assert.Nil(t, err)
	assert.Equal(t, 2, len(gotPeers))
	assert.Equal(t, []peers.Peer{{
		IP:   []byte{127, 0, 0, 1},
		Port: 6881,
	}, {
		IP:   []byte{192, 168, 0, 10},
		Port: 6969,
	}}, gotPeers)
}
