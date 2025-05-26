package peer

import (
	"bytes"
	"context"
	"crypto/sha1"
	"errors"
	"io"
	"log/slog"
	"maps"
	"math"
	"net"
	"slices"
	"time"

	"github.com/danferreira/gtorrent/internal/handshake"
	"github.com/danferreira/gtorrent/internal/message"
	"github.com/danferreira/gtorrent/internal/piece"
	"github.com/danferreira/gtorrent/internal/storage"
)

const BlockSize = 16 * 1024 // 16 KB
const MaxRequests = 30

type Connection struct {
	Peer               *Peer
	InfoHash           [20]byte
	PeerID             [20]byte
	PeerIsChoked       bool
	PeerIsInterested   bool
	PeerChokedUs       bool
	PeerInterestedUs   bool
	Conn               net.Conn
	PeerBitfield       piece.Bitfield
	OwnBitfield        *piece.Bitfield
	CanReceiveBitfield bool
	PieceLength        int
	Storage            *storage.Storage
}

func (pc *Connection) AcceptConnection(conn net.Conn) error {
	pc.Conn = conn
	slog.Info("Peer connected", "peer", pc.Peer.Addr)

	pc.PeerIsChoked = true

	return pc.sendBitfield()
}

func (pc *Connection) Connect() error {
	conn, err := net.DialTimeout("tcp4", pc.Peer.Addr, 5*time.Second)
	if err != nil {
		slog.Error("Error during connection", "error", err)
		return err
	}

	err = pc.sendHandshake(conn)
	if err != nil {
		return err
	}

	err = pc.receiveHandshake(conn)
	if err != nil {
		return err
	}

	slog.Info("Peer connected", "peer", pc.Peer.Addr)

	pc.Conn = conn
	pc.PeerChokedUs = true
	pc.CanReceiveBitfield = true

	return nil
}

func (pc *Connection) ExchangeMessages(ctx context.Context, workChan <-chan *piece.PieceWork, failChan chan<- *piece.PieceWork, resultChan chan *piece.PieceDownloaded) error {
	requests := 0

	type pendingPiece struct {
		pw   *piece.PieceWork
		data map[int][]byte
	}

	pendingPieces := make(map[int]pendingPiece)

	for {
		select {
		case <-ctx.Done():
			slog.Info("Quitting...Should send a close message to peer")
			return nil
		default:
			if !pc.PeerChokedUs && pc.PeerInterestedUs && requests < MaxRequests {
				select {
				case pw := <-workChan:
					if pc.PeerBitfield.HasPiece(pw.Index) {
						numBlocks := int(math.Ceil(float64(pw.Length) / BlockSize))

						for i := 0; i < numBlocks; i++ {
							offset := i * BlockSize
							end := offset + BlockSize

							if end > pw.Length {
								end = pw.Length
							}

							blockSize := end - offset

							err := pc.sendRequest(pw.Index, offset, blockSize)
							if err != nil {
								slog.Error("Error", "error", err)
								failChan <- pw
								break
							}
							pendingPieces[pw.Index] = pendingPiece{
								pw:   pw,
								data: make(map[int][]byte),
							}

							requests++
						}
					} else {
						failChan <- pw
					}

				default:
				}
			}

			msg, err := message.Read(pc.Conn)
			if err != nil {
				if errors.Is(err, io.EOF) {
					slog.Info("peer closed", "peer", pc.Peer.Addr)
					return nil
				}
				slog.Error("read error", "err", err)

				return err
			}

			block, err := pc.handleMessage(msg)
			if err != nil {
				return err
			}

			if block != nil {
				requests--
				pendingPiece := pendingPieces[block.index]
				pendingPiece.data[block.offset] = block.data
				numBlocks := int(math.Ceil(float64(pendingPiece.pw.Length) / BlockSize))

				if len(pendingPiece.data) == numBlocks {
					keys := slices.Sorted(maps.Keys(pendingPiece.data))

					var r []byte
					for _, k := range keys {
						r = append(r, pendingPiece.data[k]...)
					}

					if !pc.checkIntegrity(pendingPiece.pw.Hash, r) {
						slog.Warn("Invalid piece hash. Sending it back...")
						failChan <- pendingPiece.pw

						continue
					}

					resultChan <- &piece.PieceDownloaded{
						Index: block.index,
						Data:  r,
						PW:    *pendingPiece.pw,
					}
				}

			}
		}
	}
}

func (pc *Connection) checkIntegrity(expectedHash [20]byte, data []byte) bool {
	hash := sha1.Sum(data)
	return bytes.Equal(hash[:], expectedHash[:])
}

func (pc *Connection) sendHandshake(conn io.Writer) error {
	h := handshake.Handshake{
		InfoHash: pc.InfoHash,
		PeerID:   pc.PeerID,
	}

	return h.Write(conn)
}

func (pc *Connection) receiveHandshake(conn io.Reader) error {
	h, err := handshake.Read(conn)
	if err != nil {
		return err
	}

	if !bytes.Equal(pc.InfoHash[:], h.InfoHash[:]) {
		return errors.New("infoHash received on handshake is different")
	}

	return nil
}

type block struct {
	index  int
	offset int
	data   []byte
}

func (pc *Connection) handleMessage(m *message.Message) (*block, error) {
	if m == nil {
		slog.Debug("Keep Alive")
		return nil, nil
	}
	switch m.ID {
	case message.MessageChoke:
		slog.Debug("Peer Choked us")
		pc.PeerChokedUs = true
	case message.MessageUnchoke:
		slog.Debug("Peer Unchoked us")
		pc.PeerChokedUs = false
	case message.MessageInterested:
		slog.Debug("Peer is interested on us")
		pc.PeerIsInterested = true
		if err := pc.sendUnchoke(); err != nil {
			slog.Error("Error on sending unchoke", "error", err)
			return nil, nil
		}
		pc.PeerIsChoked = false
	case message.MessageNotInterest:
		slog.Info("Peer is not interested on us")
		pc.PeerIsInterested = false
	case message.MessageHave:
		slog.Info("Peer have a piece")
		index := m.ParseAsHave()
		pc.PeerBitfield.SetPiece(index)
		if !pc.PeerIsInterested && !pc.OwnBitfield.HasPiece(index) {
			pc.sendInterested()
		}

	case message.MessageBitfield:
		slog.Info("Peer sent bitfield")
		if !pc.CanReceiveBitfield {
			return nil, errors.New("late bitfield received")
		}
		pc.PeerBitfield = m.Payload
		pc.sendInterested()
	case message.MessageRequest:
		slog.Info("Peer ask for a piece")

		if pc.PeerIsChoked {
			slog.Info("Peer is choked. Should wait until unchoked")
			return nil, nil
		}

		m, err := m.ParseAsRequest()
		if err != nil {
			slog.Error("Error parsing request message", "error", err)
			return nil, nil
		}

		// read data from local storage
		data, err := pc.Storage.Read((int(m.Index)*pc.PieceLength)+int(m.Begin), int(m.Length))
		if err != nil {
			slog.Error("Error reading piece from storage", "error", err)
			return nil, nil
		}

		err = pc.sendPiece(int(m.Index), int(m.Begin), data)
		if err != nil {
			slog.Error("Error when sending piece to peer", "error", err)
			return nil, nil
		}

	case message.MessagePiece:
		index, offset, data := m.AsPiece()
		return &block{
			index:  int(index),
			offset: int(offset),
			data:   data,
		}, nil
	case message.MessageCancel:
		slog.Info("Peer wants to cancel")
	default:
		slog.Info("Message ID not implemented yet", "id", m.ID)
	}

	return nil, nil
}

func (pc *Connection) sendInterested() error {
	slog.Info("Sending interest")
	m := message.Message{
		ID: message.MessageInterested,
	}
	_, err := pc.Conn.Write(m.Serialize())

	if err != nil {
		slog.Error("Error on sending interest", "error", err)
		return err
	}

	pc.PeerInterestedUs = true

	return nil
}

func (pc *Connection) sendRequest(index, begin, length int) error {
	m := message.NewRequest(index, begin, length)
	_, err := pc.Conn.Write(m.Serialize())
	return err
}

func (pc *Connection) sendBitfield() error {
	slog.Info("Sending bitfield")
	m := message.Message{
		ID:      message.MessageBitfield,
		Payload: *pc.OwnBitfield,
	}
	_, err := pc.Conn.Write(m.Serialize())
	return err
}

func (pc *Connection) sendUnchoke() error {
	m := message.Message{
		ID: message.MessageUnchoke,
	}
	_, err := pc.Conn.Write(m.Serialize())
	return err
}

func (pc *Connection) sendPiece(index, begin int, data []byte) error {
	m := message.NewPiece(index, begin, data)
	_, err := pc.Conn.Write(m.Serialize())
	return err
}
func (pc *Connection) SendHave(index int) error {
	m := message.NewHave(index)
	_, err := pc.Conn.Write(m.Serialize())
	return err
}
