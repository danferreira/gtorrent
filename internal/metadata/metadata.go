package metadata

import (
	"bytes"
	"crypto/sha1"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jackpal/bencode-go"
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
	Length int64
}

type torrentFile struct {
	Announce string          `bencode:"announce"`
	Info     torrentFileInfo `bencode:"info"`
}

type torrentFileInfo struct {
	Name        string                `bencode:"name"`
	Pieces      string                `bencode:"pieces"`
	PieceLength int                   `bencode:"piece length"`
	Length      int64                 `bencode:"length"`
	Files       []torrentFileInfoFile `bencode:"files"`
}

type torrentFileInfoFile struct {
	Path   []string `bencode:"path"`
	Length int64    `bencode:"length"`
}

func Parse(path string) (*Metadata, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	tf := torrentFile{}
	if err = bencode.Unmarshal(file, &tf); err != nil {
		return nil, err
	}

	announceURL, err := url.Parse(tf.Announce)
	if err != nil {
		return nil, err
	}

	var files []FileInfo

	if len(tf.Info.Files) > 0 {
		for _, file := range tf.Info.Files {
			path := filepath.Join(tf.Info.Name, strings.Join(file.Path, "/"))
			fileInfo := FileInfo{
				Path:   path,
				Length: file.Length,
			}

			files = append(files, fileInfo)
		}
	} else {
		fileInfo := FileInfo{
			Path:   tf.Info.Name,
			Length: tf.Info.Length,
		}

		files = append(files, fileInfo)
	}

	chunks := slices.Collect(slices.Chunk([]byte(tf.Info.Pieces), 20))
	pieces := make([][20]byte, 0, len(chunks))

	for _, chunk := range chunks {
		var arr [20]byte
		copy(arr[:], chunk)
		pieces = append(pieces, arr)
	}

	dict := map[string]interface{}{
		"name":         tf.Info.Name,
		"pieces":       tf.Info.Pieces,
		"piece length": tf.Info.PieceLength,
	}

	// If it's single-file, we add "length"
	// If it's multi-file, we add "files"
	if len(tf.Info.Files) == 0 {
		dict["length"] = tf.Info.Length
	} else {
		dict["files"] = tf.Info.Files
	}

	var buf bytes.Buffer
	if err = bencode.Marshal(&buf, dict); err != nil {
		return nil, err
	}

	hash := sha1.Sum(buf.Bytes())

	return &Metadata{
		Announce: announceURL,
		Info: Info{
			Name:        tf.Info.Name,
			Pieces:      pieces,
			PieceLength: tf.Info.PieceLength,
			Files:       files,
			InfoHash:    hash,
		},
	}, nil
}

func (t *Info) TotalLength() int64 {
	var total int64
	for _, file := range t.Files {
		total += file.Length
	}

	return total
}
