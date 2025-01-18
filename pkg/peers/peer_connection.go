package peers

import (
	"bytes"
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"net"
	"slices"
	"time"

	"github.com/danferreira/gtorrent/pkg/handshake"
	"github.com/danferreira/gtorrent/pkg/message"
	"github.com/danferreira/gtorrent/pkg/pieces"
)

const BlockSize = 16 * 1024 // 16 KB
const MaxRequests = 30

type PeerConnection struct {
	Peer               *Peer
	InfoHash           [20]byte
	PeerID             [20]byte
	PeerIsChoked       bool
	PeerIsInterested   bool
	PeerChokedUs       bool
	PeerInterestedUs   bool
	Conn               net.Conn
	PeerBitfield       pieces.Bitfield
	OwnBitfield        *pieces.Bitfield
	PiecesChan         chan *pieces.PieceWork
	CanReceiveBitfield bool
	ResultChan         chan pieces.PieceDownloaded
}

func (pc *PeerConnection) AcceptConnection(conn net.Conn) error {
	err := pc.receiveHandshake(conn)
	if err != nil {
		return err
	}
	err = pc.sendHandshake(conn)
	if err != nil {
		return err
	}

	pc.Conn = conn
	pc.PeerIsChoked = true

	fmt.Println("Peer connected: ", pc.Peer.Addr)

	return pc.sendBitfield()
}

func (pc *PeerConnection) Connect(done chan bool) error {
	conn, err := net.DialTimeout("tcp", pc.Peer.Addr, 5*time.Second)
	if err != nil {
		fmt.Println("Error: ", err)
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

	fmt.Println("Peer connected: ", pc.Peer.Addr)

	pc.Conn = conn
	pc.PeerChokedUs = true
	pc.CanReceiveBitfield = true

	return nil
}

func (pc *PeerConnection) ExchangeMessages(done chan bool) error {
	requests := 0

	type pendingPiece struct {
		pw   *pieces.PieceWork
		data map[int][]byte
	}

	pendingPieces := make(map[int]pendingPiece)

outerLoop:
	for {
		select {
		case <-done:
			fmt.Println("Quitting...Should send a close message to peer")
			break outerLoop
		default:
			if !pc.PeerChokedUs && pc.PeerInterestedUs && requests < MaxRequests {
				select {
				case pw := <-pc.PiecesChan:
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
								fmt.Println("Error: ", err)
								pc.PiecesChan <- pw
								break
							}
							pendingPieces[pw.Index] = pendingPiece{
								pw:   pw,
								data: make(map[int][]byte),
							}

							requests++
						}
					} else {
						fmt.Println("Peer has not this piece")
						pc.PiecesChan <- pw
					}

				default:
					// fmt.Println("There is no more pieces to download")
				}
			}

			msg, err := message.Read(pc.Conn)
			if err != nil {
				fmt.Println("Error: ", err)
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
						fmt.Println("Invalid piece hash. Sending it back...")
						pc.PiecesChan <- pendingPiece.pw
					}

					pc.ResultChan <- pieces.PieceDownloaded{
						Index: block.index,
						Data:  r,
					}
				}

			}
		}
	}

	return nil
}

func (pc *PeerConnection) checkIntegrity(expectedHash [20]byte, data []byte) bool {
	hash := sha1.Sum(data)
	return bytes.Equal(hash[:], expectedHash[:])
}

func (pc *PeerConnection) sendHandshake(conn io.Writer) error {
	h := handshake.Handshake{
		InfoHash: pc.InfoHash,
		PeerID:   pc.PeerID,
	}

	return h.Write(conn)
}

func (pc *PeerConnection) receiveHandshake(conn io.Reader) error {
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

func (pc *PeerConnection) handleMessage(m *message.Message) (*block, error) {
	if m == nil {
		fmt.Println("Keep Alive")
		return nil, nil
	}
	switch m.ID {
	case message.MessageChoke:
		fmt.Println("Peer Choked us")
		pc.PeerChokedUs = true
	case message.MessageUnchoke:
		fmt.Println("Peer Unchoked us")
		pc.PeerChokedUs = false
	case message.MessageInterested:
		fmt.Println("Peer is interested on us")
		pc.PeerIsInterested = true
		pc.sendUnchoke()
	case message.MessageNotInterest:
		fmt.Println("Peer is not interested on us")
		pc.PeerIsInterested = false
	case message.MessageHave:
		fmt.Println("Peer have a piece")
	case message.MessageBitfield:
		fmt.Println("Peer sent bitfield")
		if !pc.CanReceiveBitfield {
			return nil, errors.New("late bitfield received")
		}
		pc.PeerBitfield = m.Payload
		pc.sendInterested()
	case message.MessageRequest:
		fmt.Println("Peer ask for a piece")
	case message.MessagePiece:
		index, offset, data := m.AsPiece()
		return &block{
			index:  int(index),
			offset: int(offset),
			data:   data,
		}, nil
	case message.MessageCancel:
		fmt.Println("Peer wants to cancel")
	default:
		fmt.Printf("ID: %d Not implemented yet", m.ID)
	}

	return nil, nil
}

func (pc *PeerConnection) sendInterested() error {
	fmt.Println("Sending interest")
	m := message.Message{
		ID: message.MessageInterested,
	}
	_, err := pc.Conn.Write(m.Serialize())

	if err != nil {
		fmt.Println("Error on sending interest", err)
		return err
	}

	pc.PeerInterestedUs = true

	return nil
}

func (pc *PeerConnection) sendRequest(index, begin, length int) error {
	m := message.NewRequest(index, begin, length)
	_, err := pc.Conn.Write(m.Serialize())
	return err
}

func (pc *PeerConnection) sendBitfield() error {
	m := message.Message{
		ID:      message.MessageBitfield,
		Payload: *pc.OwnBitfield,
	}
	_, err := pc.Conn.Write(m.Serialize())
	return err
}

func (pc *PeerConnection) sendUnchoke() error {
	m := message.Message{
		ID: message.MessageUnchoke,
	}
	_, err := pc.Conn.Write(m.Serialize())
	return err
}
