package storage

import (
	"bytes"
	"crypto/sha1"
	"testing"

	"github.com/danferreira/gtorrent/internal/bitfield"
	"github.com/danferreira/gtorrent/internal/metadata"
	"github.com/stretchr/testify/require"
)

type MockStorage struct {
	buf []byte
}

func (m *MockStorage) ReadAt(p []byte, off int64) (n int, err error) {
	copy(p, m.buf[off:])
	return len(p), nil
}

func TestScanDisk(t *testing.T) {
	pieceLen := 16384
	phGood := sha1.Sum(bytes.Repeat([]byte{1}, pieceLen))

	cases := []struct {
		name     string
		dataOff  int64
		wantBits bitfield.Bitfield
	}{
		{"first piece", 0, bitfield.Bitfield{0b10000000}},
		{"last piece", 3 * int64(pieceLen), bitfield.Bitfield{0b00010000}},
		{"none", -1, bitfield.Bitfield{0b00000000}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf := make([]byte, 4*pieceLen)
			if tc.dataOff >= 0 {
				copy(buf[tc.dataOff:], bytes.Repeat([]byte{1}, pieceLen))
			}
			st := &MockStorage{buf}

			md := &metadata.Metadata{
				Info: metadata.Info{
					PieceLength: pieceLen,
					Pieces:      [][20]byte{phGood, phGood, phGood, phGood},
					Files:       []metadata.FileInfo{{Length: int64(len(buf))}},
				},
			}

			got, err := NewManager(st).ScanDisk(md)
			require.NoError(t, err)
			require.Equal(t, tc.wantBits, got)
		})
	}
}
