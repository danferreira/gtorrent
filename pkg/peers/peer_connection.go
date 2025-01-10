package peers

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"
)

type PeerConnection struct {
	Peer     Peer
	InfoHash [20]byte
	PeerID   [20]byte
}

func (pc *PeerConnection) Connect() error {
	addr := net.JoinHostPort(pc.Peer.IP.String(), strconv.Itoa(int(pc.Peer.Port)))
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		fmt.Println("Error:", err)
		return err
	}

	defer conn.Close()

	err = pc.sendHandshake(conn)
	if err != nil {
		return err
	}

	err = pc.receiveHandshake(conn)
	if err != nil {
		return err
	}

	return nil
}

func (pc *PeerConnection) sendHandshake(conn io.Writer) error {
	h := Handshake{
		InfoHash: pc.InfoHash,
		PeerID:   pc.PeerID,
	}

	_, err := conn.Write(h.Serialize())

	if err != nil {
		fmt.Println("Error: ", err)
		return err
	}

	return nil
}

func (pc *PeerConnection) receiveHandshake(conn io.Reader) error {
	buf := make([]byte, 1)

	_, err := io.ReadFull(conn, buf)

	if err != nil {
		fmt.Println("Error: ", err)
		return err
	}

	if int(buf[0]) != 19 {
		fmt.Println("Invalid protocol length in handshake")
		return nil
	}

	buf = make([]byte, 19)
	_, err = io.ReadFull(conn, buf)
	if err != nil {
		fmt.Println("Error: ", err)
		return err
	}

	if string(buf) != ProtocolIdentifier {
		fmt.Println("Invalid protocol identifier in handshake")
		return err
	}

	_, err = io.CopyN(io.Discard, conn, 8)
	if err != nil {
		fmt.Println("Error: ", err)
		return err
	}

	var infoHash, peerID [20]byte

	io.ReadFull(conn, infoHash[:])
	io.ReadFull(conn, peerID[:])

	if !bytes.Equal(pc.InfoHash[:], infoHash[:]) {
		fmt.Println("InfoHash received on handshake is different")
		return nil
	}

	return nil
}
