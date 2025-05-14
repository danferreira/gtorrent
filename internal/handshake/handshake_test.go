package handshake

import (
	"bytes"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSerialize(t *testing.T) {
	h := Handshake{
		InfoHash: [20]byte{20, 19, 18, 17, 16, 15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1},
		PeerID:   [20]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20},
	}

	serialized := h.Serialize()

	assert.Equal(t, []byte{19, 66, 105, 116, 84, 111, 114, 114, 101, 110, 116, 32, 112, 114, 111, 116, 111, 99, 111, 108, 0, 0, 0, 0, 0, 0, 0, 0, 20, 19, 18, 17, 16, 15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}, serialized)
}

func TestRead(t *testing.T) {
	infoHash := [20]byte{8}
	peerID := [20]byte{9}

	tests := map[string]struct {
		input      []byte
		output     *Handshake
		shouldFail bool
	}{
		"mal constructed buffer":      {[]byte{0}, nil, true},
		"invalid protocol length":     {[]byte{18}, nil, true},
		"invalid protocol identifier": {append([]byte{19}, []byte("Some Other protocol")...), nil, true},
		"valid handshake": {slices.Concat([]byte{19}, []byte("BitTorrent protocol"), []byte{0, 0, 0, 0, 0, 0, 0, 0}, infoHash[:], peerID[:]), &Handshake{
			InfoHash: infoHash,
			PeerID:   peerID,
		}, false},
	}

	for _, tt := range tests {
		buff := bytes.NewBuffer(tt.input)

		h, err := Read(buff)

		if tt.shouldFail {
			assert.NotNil(t, err)
		} else {
			assert.Nil(t, err)
			assert.Equal(t, tt.output, h)
		}
	}
}

func TestWrite(t *testing.T) {
	h := Handshake{
		InfoHash: [20]byte{8},
		PeerID:   [20]byte{9},
	}

	buffer := new(bytes.Buffer)
	err := h.Write(buffer)

	assert.Nil(t, err)
	assert.Equal(t, slices.Concat([]byte{19}, []byte("BitTorrent protocol"), []byte{0, 0, 0, 0, 0, 0, 0, 0}, h.InfoHash[:], h.PeerID[:]), buffer.Bytes())
}
