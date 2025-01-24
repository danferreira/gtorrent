package metadata

import (
	"net/url"
)

type Metadata struct {
	Announce *url.URL
	Info     Info
}

type Info struct {
	Name        string
	Pieces      [][20]byte
	PieceLength int
	Files       []FileInfo
	InfoHash    [20]byte
}

type FileInfo struct {
	Path   string
	Length int
}

func (t *Info) TotalLength() int {
	total := 0
	for _, file := range t.Files {
		total += file.Length
	}

	return total
}
