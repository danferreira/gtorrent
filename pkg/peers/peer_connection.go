package peers

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"sort"
	"time"

	"github.com/danferreira/gtorrent/pkg/handshake"
	"github.com/danferreira/gtorrent/pkg/message"
	"github.com/danferreira/gtorrent/pkg/pieces"
)

const BlockSize = 16 * 1024 // 16 KB

type PeerConnection struct {
	Peer               *Peer
	InfoHash           [20]byte
	PeerID             [20]byte
	Choked             bool
	Conn               net.Conn
	Bitfield           pieces.Bitfield
	PiecesChan         chan *pieces.PieceWork
	CanReceiveBitfield bool
}

func (pc *PeerConnection) Connect(done chan bool) error {
	addr := pc.Peer.Addr()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
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

	fmt.Println("Peer connected: ", addr)

	pc.Conn = conn
	pc.Choked = true
	pc.CanReceiveBitfield = true

	pc.sendInterested()

	return nil
}

func (pc *PeerConnection) AttemptDownloadPiece(pw *pieces.PieceWork, done chan bool) ([]byte, error) {
	numBlocks := pw.Length / BlockSize
	if pw.Length%BlockSize > 0 {
		numBlocks++
	}

	blocks := make(map[int]int)
	data := make(map[int][]byte)

	for i := 0; i < numBlocks; i++ {
		begin := i * BlockSize
		end := begin + BlockSize

		if end > pw.Length {
			end = pw.Length
		}

		blockSize := end - begin
		blocks[begin] = blockSize
	}

	results := make(chan block, numBlocks)

	nRequests := 0
	pending := 0
outerLoop:
	for {
		select {
		case result := <-results:
			data[result.offset] = result.data

			nRequests--
			if len(data) == numBlocks {
				keys := make([]int, 0, len(data))
				for k := range data {
					keys = append(keys, k)
				}
				sort.Ints(keys)

				var r []byte
				for _, k := range keys {
					r = append(r, data[k]...)
				}

				return r, nil
			}
		case <-done:
			break outerLoop
		default:
			if !pc.Choked && pending < len(blocks) {
				for offset, blockSize := range blocks {
					if nRequests >= 5 {
						break
					}

					err := pc.sendRequest(pw.Index, offset, blockSize)
					if err != nil {
						fmt.Println("Error: ", err)
						break
					}

					nRequests++
					pending++
				}
			}

			msg, err := message.Read(pc.Conn)
			if err != nil {
				fmt.Println("Error: ", err)
				return nil, err
			}

			err = pc.handleMessage(msg, results)
			if err != nil {
				return nil, err
			}
			pc.CanReceiveBitfield = false
		}
	}

	return nil, nil
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
	offset int
	data   []byte
}

func (pc *PeerConnection) handleMessage(m *message.Message, results chan block) error {
	if m == nil {
		fmt.Println("Keep Alive")
		return nil
	}
	switch m.ID {
	case message.MessageChoke:
		fmt.Println("Peer Choked us")
		pc.Choked = true
	case message.MessageUnchoke:
		fmt.Println("Peer Unchoked us")
		pc.Choked = false
	case message.MessageNotInterest:
		fmt.Println("Peer is not interested in us")
	case message.MessageHave:
		fmt.Println("Peer have a piece")
	case message.MessageBitfield:
		fmt.Println("Peer sent bitfield")
		if !pc.CanReceiveBitfield {
			return errors.New("late bitfield received")
		}
		pc.Bitfield = m.Payload
	case message.MessageRequest:
		fmt.Println("Peer ask for a piece")
	case message.MessagePiece:
		offset, data := m.AsPiece()
		results <- block{
			offset: int(offset),
			data:   data,
		}
	case message.MessageCancel:
		fmt.Println("Peer wants to cancel")
	default:
		fmt.Printf("ID: %d Not implemented yet", m.ID)
	}

	return nil
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

	return nil
}
func (pc *PeerConnection) sendRequest(index, begin, length int) error {
	m := message.NewRequest(index, begin, length)
	_, err := pc.Conn.Write(m.Serialize())
	return err
}
