package metadata

import (
	"net/url"
	"os"
	"slices"
	"strings"

	"github.com/jackpal/bencode-go"
)

type Metadata struct {
	Announce *url.URL
	Info     MetadataInfo
}

type MetadataInfo struct {
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
			file_info := FileInfo{
				Path:   strings.Join(file.Path, "/"),
				Length: file.Length,
			}

			files = append(files, file_info)
		}
	} else {
		file_info := FileInfo{
			Path:   tf.Info.Name,
			Length: tf.Info.Length,
		}

		files = append(files, file_info)
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
