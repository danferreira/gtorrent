package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/danferreira/gtorrent/internal/metadata"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewStorage(t *testing.T) {
	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "file_1.txt")

	file := metadata.FileInfo{
		Path:   filePath,
		Length: 100,
	}

	storage, err := NewStorage([]metadata.FileInfo{file})
	assert.Nil(t, err)

	defer storage.CloseFiles()

	assert.Equal(t, file.Path, storage.Files[0].Path)
	assert.Equal(t, file.Length, storage.Files[0].Length)
	_, err = os.Stat(storage.Files[0].Path)
	assert.Nil(t, err)
}

func TestReadAt(t *testing.T) {
	tmp := t.TempDir()
	fo1, _ := os.Create(filepath.Join(tmp, "read_1.txt"))
	fo1.Write([]byte("abcde"))
	fo2, _ := os.Create(filepath.Join(tmp, "read_2.txt"))
	fo2.Write([]byte("fghij"))

	files := []metadata.FileInfo{{
		Path:   filepath.Join(tmp, "read_1.txt"),
		Length: 5,
	}, {
		Path:   filepath.Join(tmp, "read_2.txt"),
		Length: 5,
	}}

	tests := map[string]struct {
		start    int64
		len      int
		expected string
	}{
		"read from first file":           {0, 5, "abcde"},
		"read from second file":          {5, 5, "fghij"},
		"partially read from both files": {3, 5, "defgh"},
	}
	storage, err := NewStorage(files)
	assert.NoError(t, err)

	defer storage.CloseFiles()

	for _, tt := range tests {
		buf := make([]byte, tt.len)
		v, err := storage.ReadAt(buf, tt.start)
		assert.NoError(t, err)
		assert.Equal(t, tt.len, v)
		assert.Equal(t, []byte(tt.expected), buf)
	}
}

func TestWriteAt(t *testing.T) {
	tmp := t.TempDir()

	files := []metadata.FileInfo{{
		Path:   filepath.Join(tmp, "write_1.txt"),
		Length: 5,
	}, {
		Path:   filepath.Join(tmp, "write_2.txt"),
		Length: 5,
	}, {
		Path:   filepath.Join(tmp, "write_3.txt"),
		Length: 5,
	}}

	storage, err := NewStorage(files)
	require.NoError(t, err)

	defer storage.CloseFiles()

	storage.WriteAt([]byte("cdefghijklmn"), 2)

	buf := make([]byte, 5)
	storage.Files[0].File.ReadAt(buf, 0)
	assert.Equal(t, []byte("\x00\x00cde"), buf)

	buf = make([]byte, 5)
	storage.Files[1].File.ReadAt(buf, 0)
	assert.Equal(t, []byte("fghij"), buf)

	buf = make([]byte, 5)
	storage.Files[2].File.ReadAt(buf, 0)
	assert.Equal(t, []byte("klmn\x00"), buf)
}
