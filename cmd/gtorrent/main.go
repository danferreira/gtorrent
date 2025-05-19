package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/danferreira/gtorrent/internal/engine"
)

func main() {
	h := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	slog.SetDefault(slog.New(h))

	path := flag.String("file", "", "The torrent file")
	flag.Parse()

	e := engine.NewEngine()

	e.AddFile(*path)

	for {
		select {
		case stats := <-e.Stats():
			downloaded, _, _, size := stats.GetSnapshot()
			percent := (float64(downloaded) / float64(size)) * 100
			fmt.Printf("Downloading:  %.2f%% of %d\n", percent, size)

		}
	}
}
