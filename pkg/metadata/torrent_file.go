package metadata

import (
	"bytes"
	"crypto/sha1"
	"net/url"
	"os"
	"slices"
	"strings"

	"github.com/jackpal/bencode-go"
)

type TorrentFile struct {
	Announce string          `bencode:"announce"`
	Info     TorrentFileInfo `bencode:"info"`
}

type TorrentFileInfo struct {
	Name        string                `bencode:"name"`
	Pieces      string                `bencode:"pieces"`
	PieceLength int                   `bencode:"piece length"`
	Length      int                   `bencode:"length"`
	Files       []TorrentFileInfoFile `bencode:"files"`
}

type TorrentFileInfoFile struct {
	Path   []string `bencode:"path"`
	Length int      `bencode:"length"`
}

func (info *TorrentFileInfo) hash() ([20]byte, error) {
	dict := map[string]interface{}{
		"name":         info.Name,
		"pieces":       info.Pieces,
		"piece length": info.PieceLength,
	}

	// If it's single-file, we add "length"
	// If it's multi-file, we add "files"
	if len(info.Files) == 0 {
		dict["length"] = info.Length
	} else {
		dict["files"] = info.Files
	}

	var buf bytes.Buffer
	err := bencode.Marshal(&buf, dict)

	if err != nil {
		return [20]byte{}, err
	}

	return sha1.Sum(buf.Bytes()), nil
}

func Parse(path string) (Metadata, error) {
	file, err := os.Open(path)
	if err != nil {
		return Metadata{}, err
	}
	defer file.Close()

	tf := TorrentFile{}
	err = bencode.Unmarshal(file, &tf)

	if err != nil {
		return Metadata{}, err
	}

	announceUrl, err := url.Parse(tf.Announce)

	if err != nil {
		return Metadata{}, err
	}

	var files []FileInfo

	if len(tf.Info.Files) > 0 {
		for _, file := range tf.Info.Files {
			fileInfo := FileInfo{
				Path:   strings.Join(file.Path, "/"),
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

	buf := []byte(tf.Info.Pieces)
	chunks := slices.Collect(slices.Chunk(buf, 20))
	pieces := make([][20]byte, 0, len(chunks))

	for _, chunk := range chunks {
		var arr [20]byte
		copy(arr[:], chunk)
		pieces = append(pieces, arr)
	}

	infoHash, err := tf.Info.hash()

	if err != nil {
		return Metadata{}, err
	}

	return Metadata{
		Announce: announceUrl,
		Info: MetadataInfo{
			Name:        tf.Info.Name,
			Pieces:      pieces,
			PieceLength: tf.Info.PieceLength,
			Files:       files,
			InfoHash:    infoHash,
		},
	}, nil
}
