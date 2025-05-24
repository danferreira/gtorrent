package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/danferreira/gtorrent/internal/torrent"
)

func main() {
	h := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	slog.SetDefault(slog.New(h))

	path := flag.String("file", "", "The torrent file")
	flag.Parse()

	c := torrent.NewClient()

	c.AddFile(*path)

	for {
		select {
		case stats := <-c.Stats():
			snap := stats.Snapshot()
			percent := (float64(snap.Downloaded) / float64(snap.Size)) * 100
			fmt.Printf("Downloading:  %.2f%% of %d\n", percent, snap.Size)
		}
	}
}
