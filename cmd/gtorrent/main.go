package main

import (
	"flag"
	"fmt"
	"log"

	"github.com/danferreira/gtorrent/internal/metadata"
	"github.com/danferreira/gtorrent/internal/torrent"
)

func main() {
	path := flag.String("file", "", "The torrent file")
	flag.Parse()

	m, err := metadata.Parse(*path)

	if err != nil {
		log.Fatal(err)
	}
	var peerID [20]byte
	copy(peerID[:], "-GT0001-abcdefgh1234")

	t := torrent.Torrent{
		Metadata: *m,
		PeerID:   peerID,
	}

	err = t.Run()

	if err != nil {
		fmt.Print(err)
	}
}
