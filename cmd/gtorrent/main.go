package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/danferreira/gtorrent/internal/torrent"
)

func main() {
	h := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	slog.SetDefault(slog.New(h))

	path := flag.String("file", "", "The torrent file")
	flag.Parse()

	c := torrent.NewClient(6881)

	c.AddFile(*path)
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-sigs:
			fmt.Println("Interrupted")
			c.Close()
			os.Exit(1)
		case stats := <-c.Stats():
			snap := stats.Snapshot()
			percent := (float64(snap.Downloaded) / float64(snap.Size)) * 100
			fmt.Printf("Downloading:  %.2f%% of %d\n", percent, snap.Size)
		}
	}
}
