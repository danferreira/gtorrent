package storage

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/danferreira/gtorrent/pkg/metadata"
)

type File struct {
	File   *os.File
	Path   string
	Length int
}
type Storage struct {
	Files []File
}

func NewStorage(files []metadata.FileInfo) (*Storage, error) {
	var sFiles []File

	for _, f := range files {
		dir := filepath.Dir(f.Path)

		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create dirs for %s: %w", f.Path, err)
		}
		file, err := os.OpenFile(f.Path, os.O_CREATE|os.O_RDWR, 0644)
		if err != nil {
			return nil, err
		}
		sFiles = append(sFiles, File{
			File:   file,
			Path:   f.Path,
			Length: f.Length,
		})
	}

	return &Storage{
		Files: sFiles,
	}, nil
}

func (s *Storage) Read(start int, length int) ([]byte, error) {
	var offset int
	end := start + length
	buffer := new(bytes.Buffer)
	remaining := length

	for _, file := range s.Files {
		nextOffset := offset + file.Length
		if start >= nextOffset {
			offset += file.Length
			continue
		}

		actualStart := start - offset
		if actualStart < 0 {
			actualStart = 0
		}

		st, err := file.File.Stat()
		if err != nil {
			return nil, err
		}

		if st.Size() <= int64(actualStart) {
			continue
		}

		actualEnd := file.Length
		if end <= nextOffset {
			actualEnd = end - offset
		}

		offsetLength := actualEnd - actualStart

		chunk := make([]byte, offsetLength)

		_, err = file.File.ReadAt(chunk, int64(actualStart))

		if err != nil {
			fmt.Println(err)
			return nil, err
		}
		buffer.Write(chunk)

		remaining -= offsetLength
		if remaining == 0 {
			break
		}

		offset += file.Length
	}

	return buffer.Bytes(), nil
}

func (s *Storage) Write(start int, data []byte) error {
	var offset, cursor int

	for _, file := range s.Files {
		nextOffset := offset + file.Length
		if start >= nextOffset {
			offset += file.Length
			continue
		}

		actualStart := start - offset
		if actualStart < 0 {
			actualStart = 0
		}

		var actualData []byte
		if (actualStart + len(data[cursor:])) > file.Length {
			amount := file.Length - actualStart
			actualData = data[cursor:amount]
		} else {
			actualData = data[cursor:]
		}

		cursor = cursor + len(actualData)

		_, err := file.File.WriteAt(actualData, int64(actualStart))

		if err != nil {
			return err
		}

		if cursor == len(data) {
			break
		}

		offset += file.Length
	}

	return nil
}

func (s *Storage) CloseFiles() error {
	var err error
	for _, sf := range s.Files {
		e := sf.File.Close()
		if e != nil && err == nil {
			// record the first error encountered
			err = e
		}
	}
	return err
}
