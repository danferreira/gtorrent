package handshake

import (
	"errors"
	"fmt"
	"io"
)

const ProtocolIdentifier = "BitTorrent protocol"

type Handshake struct {
	InfoHash [20]byte
	PeerID   [20]byte
}

func (h *Handshake) Serialize() []byte {
	buf := make([]byte, len(ProtocolIdentifier)+49)
	buf[0] = byte(len(ProtocolIdentifier))
	curr := 1
	curr += copy(buf[curr:], []byte(ProtocolIdentifier))
	curr += copy(buf[curr:], make([]byte, 8)) //Reserved for Flags
	curr += copy(buf[curr:], h.InfoHash[:])
	copy(buf[curr:], h.PeerID[:])
	return buf
}

func (h *Handshake) Write(writer io.Writer) error {
	_, err := writer.Write(h.Serialize())
	return err
}

func Read(reader io.Reader) (*Handshake, error) {
	buf := make([]byte, 1)

	_, err := io.ReadFull(reader, buf)

	if err != nil {
		return nil, fmt.Errorf("invalid handshake length: %w", err)
	}

	if int(buf[0]) != 19 {
		return nil, errors.New("invalid protocol length in handshake")
	}

	buf = make([]byte, 19)
	_, err = io.ReadFull(reader, buf)
	if err != nil {
		return nil, errors.New("invalid protocol in handshake")
	}

	if string(buf) != ProtocolIdentifier {
		return nil, errors.New("invalid protocol identifier in handshake")
	}

	_, err = io.CopyN(io.Discard, reader, 8)
	if err != nil {
		return nil, err
	}

	var infoHash, peerID [20]byte

	_, err = io.ReadFull(reader, infoHash[:])
	if err != nil {
		return nil, err
	}

	_, err = io.ReadFull(reader, peerID[:])
	if err != nil {
		return nil, err
	}

	return &Handshake{
		InfoHash: infoHash,
		PeerID:   peerID,
	}, nil
}
