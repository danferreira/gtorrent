package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/danferreira/gtorrent/pkg/metadata"
	"github.com/stretchr/testify/assert"
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

func TestRead(t *testing.T) {
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
		start    int
		len      int
		expected string
	}{
		"read from first file":           {0, 5, "abcde"},
		"read from second file":          {5, 5, "fghij"},
		"partially read from both files": {3, 5, "defgh"},
	}
	storage, err := NewStorage(files)
	assert.Nil(t, err)

	defer storage.CloseFiles()

	for _, tt := range tests {
		v, err := storage.Read(tt.start, tt.len)
		assert.Nil(t, err)
		assert.Equal(t, []byte(tt.expected), v)
	}
}

func TestWrite(t *testing.T) {
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
	assert.Nil(t, err)

	defer storage.CloseFiles()

	storage.Write(0, []byte("abcde"))
	storage.Write(5, []byte("fghij"))
	storage.Write(10, []byte("klmno"))
	storage.Write(3, []byte("xxxxx"))
	storage.Write(8, []byte("zzzzz"))

	buf := make([]byte, 5)
	storage.Files[0].File.ReadAt(buf, 0)
	assert.Equal(t, []byte("abcxx"), buf)

	buf = make([]byte, 5)
	storage.Files[1].File.ReadAt(buf, 0)
	assert.Equal(t, []byte("xxxzz"), buf)

	buf = make([]byte, 5)
	storage.Files[2].File.ReadAt(buf, 0)
	assert.Equal(t, []byte("zzzno"), buf)
}
