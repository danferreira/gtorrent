package storage

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/danferreira/gtorrent/internal/metadata"
)

type File struct {
	File   *os.File
	Path   string
	Length int64
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

func (s *Storage) ReadAt(buf []byte, start int64) (int, error) {
	if len(buf) == 0 {
		return 0, nil
	}

	var offset int64
	var n int
	remaining := int64(len(buf))
	end := start + int64(len(buf))

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

		actualEnd := file.Length
		if end <= nextOffset {
			actualEnd = end - offset
		}

		amount := actualEnd - actualStart
		if amount <= 0 {
			offset += file.Length
			continue
		}
		m, err := file.File.ReadAt(buf[n:n+int(amount)], actualStart)
		n += m
		if err != nil && !errors.Is(err, io.EOF) {
			return n, err
		}

		remaining -= int64(m)

		if remaining == 0 {
			return n, nil
		}

		offset += file.Length
	}

	return n, io.EOF
}

func (s *Storage) WriteAt(data []byte, start int64) (int, error) {
	var offset, cursor int64
	var n int

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
		if (actualStart + int64(len(data[cursor:]))) > file.Length {
			amount := file.Length - actualStart
			actualData = data[cursor : cursor+amount]
		} else {
			actualData = data[cursor:]
		}

		m, err := file.File.WriteAt(actualData, int64(actualStart))
		if err != nil {
			return n, err
		}

		n += m

		cursor += int64(len(actualData))
		if cursor == int64(len(data)) {
			break
		}

		offset += file.Length
	}

	return n, nil
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
