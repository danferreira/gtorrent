package peer

import (
	"crypto/rand"
	"encoding/hex"
	"log"
)

type PeerID = [20]byte

func NewPeerID() PeerID {
	const prefix = "-GT0001-"
	var id [20]byte
	copy(id[:], prefix)

	// 12 random bytes -> 24 hex chars, but we need 12 *ASCII* bytes.
	var tail [12]byte
	if _, err := rand.Read(tail[:]); err != nil {
		log.Fatal(err) // rand failure is unrecoverable here
	}

	// Encode to printable ASCII using hex (2 chars per byte) and take first 12.
	hex.Encode(id[8:], tail[:6]) // 6 bytes * 2 hex chars = 12 ASCII bytes

	return id
}
