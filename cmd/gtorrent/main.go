package main

import (
	"flag"
	"fmt"
	"log"

	"github.com/danferreira/gtorrent/pkg/metadata"
)

func main() {
	path := flag.String("file", "", "The torrent file")
	flag.Parse()

	m, err := metadata.Parse(*path)

	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Announce url: %s", m.Announce)
}
