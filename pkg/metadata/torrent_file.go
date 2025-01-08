package metadata

import (
	"bytes"
	"crypto/sha1"

	"github.com/jackpal/bencode-go"
)

type TorrentFile struct {
	Announce string
	Info     TorrentFileInfo
}

type TorrentFileInfo struct {
	Name        string
	Pieces      string
	PieceLength int `bencode:"piece length"`
	Length      int
	Files       []TorrentFileInfoFile
}

type TorrentFileInfoFile struct {
	Path   []string
	Length int
}

func (info *TorrentFileInfo) hash() ([20]byte, error) {
	var buf bytes.Buffer
	err := bencode.Marshal(&buf, *info)

	if err != nil {
		return [20]byte{}, err
	}

	hasher := sha1.New()
	hasher.Write(buf.Bytes())

	return sha1.Sum(buf.Bytes()), nil

}
