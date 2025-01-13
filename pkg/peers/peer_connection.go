package peers

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/danferreira/gtorrent/pkg/handshake"
	"github.com/danferreira/gtorrent/pkg/message"
)

type PeerConnection struct {
	Peer     *Peer
	InfoHash [20]byte
	PeerID   [20]byte
	Choked   bool
}

func (pc *PeerConnection) Connect() error {
	addr := pc.Peer.Addr()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		fmt.Println("Error: ", err)
		return err
	}

	fmt.Println("Peer connected: ", addr)

	defer conn.Close()

	err = pc.sendHandshake(conn)
	if err != nil {
		return err
	}

	err = pc.receiveHandshake(conn)
	if err != nil {
		return err
	}

	for {
		msg, err := message.Read(conn)
		if err != nil {
			return err
		}
		pc.handleMessage(msg)
	}
}

func (pc *PeerConnection) sendHandshake(conn io.Writer) error {
	h := handshake.Handshake{
		InfoHash: pc.InfoHash,
		PeerID:   pc.PeerID,
	}

	err := h.Write(conn)

	if err != nil {
		return err
	}

	return nil
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

func (pc *PeerConnection) handleMessage(m *message.Message) {
	if m == nil {
		fmt.Println("Keep Alive")
		return
	}
	switch m.MessageID {
	case message.MessageChoke:
		fmt.Println("Peer Choked us")
		pc.Choked = true
	case message.MessageUnchoke:
		fmt.Println("Peer Unchoked us")
		pc.Choked = false
		pc.sendMessage(message.MessageInterested)
	case message.MessageNotInterest:
		fmt.Println("Peer is not interested in us")
	case message.MessageHave:
		fmt.Println("Peer have a piece")
	case message.MessageBitfield:
		fmt.Println("Peer sent bitfield")
	case message.MessageRequest:
		fmt.Println("Peer ask for a piece")
	case message.MessagePiece:
		fmt.Println("Peer sent a piece")
	case message.MessageCancel:
		fmt.Println("Peer wants to cancel")
	default:
		fmt.Printf("MessageID: %d Not implemented yet", m.MessageID)
	}
}

func (pc *PeerConnection) sendMessage(mt message.MessageID) {

}
