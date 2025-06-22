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
		Level: slog.LevelDebug,
	})
	slog.SetDefault(slog.New(h))

	path := flag.String("file", "", "The torrent file")
	flag.Parse()

	c := torrent.NewClient(torrent.Config{ListenPort: 6881})

	t, _ := c.AddFile(*path)
	c.StartTorrent(t.Metadata.Info.InfoHash)

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	<-sigs
	fmt.Println("Interrupted")
	c.Close()
	os.Exit(1)
}
