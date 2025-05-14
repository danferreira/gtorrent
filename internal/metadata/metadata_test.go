package metadata

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseSingleFileTorrent(t *testing.T) {
	metadata, err := Parse("testdata/single_file.torrent")

	assert.Nil(t, err)
	assert.Equal(t, "http://localhost:8000/announce", metadata.Announce.String())
	assert.Equal(t, "file_1.txt", metadata.Info.Name)
	assert.Equal(t, 1, len(metadata.Info.Files))
	assert.Equal(t, 405, len(metadata.Info.Pieces))
	assert.Equal(t, 32768, metadata.Info.PieceLength)
	assert.Equal(t, "file_1.txt", metadata.Info.Files[0].Path)

	var expectedHash = [20]byte{0xba, 0x3e, 0xfa, 0xe8, 0xc4, 0xd8, 0x4a, 0xad, 0xbf, 0xca, 0xc7, 0x56, 0xf7, 0xee, 0xb5, 0x6b, 0x9e, 0x1f, 0xc2, 0x21}
	assert.Equal(t, expectedHash, metadata.Info.InfoHash)
}

func TestParseMultiFileTorrent(t *testing.T) {
	metadata, err := Parse("testdata/multi_files.torrent")

	assert.Nil(t, err)
	assert.Equal(t, "http://localhost:8000/announce", metadata.Announce.String())
	assert.Equal(t, "files", metadata.Info.Name)
	assert.Equal(t, 3, len(metadata.Info.Files))
	assert.Equal(t, 658, len(metadata.Info.Pieces))
	assert.Equal(t, 32768, metadata.Info.PieceLength)
	assert.Equal(t, "files/file_1.txt", metadata.Info.Files[0].Path)
	assert.Equal(t, "files/file_2.txt", metadata.Info.Files[1].Path)
	assert.Equal(t, "files/file_3.txt", metadata.Info.Files[2].Path)

	var expectedHash = [20]byte{0x1c, 0x78, 0xe7, 0x26, 0x0d, 0x0b, 0xcf, 0x7f, 0xa2, 0x72, 0xea, 0xd1, 0xef, 0xc8, 0x47, 0x9c, 0xc4, 0xaa, 0x84, 0xf0}
	assert.Equal(t, expectedHash, metadata.Info.InfoHash)
}

func TestTotalLength(t *testing.T) {
	metadata := Metadata{
		Info: Info{
			Files: []FileInfo{
				{
					Length: 1024000,
				},
				{
					Length: 512000,
				},
			},
		},
	}

	assert.Equal(t, 1536000, metadata.Info.TotalLength())
}
