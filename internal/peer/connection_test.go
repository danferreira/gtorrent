package peer

import (
	"bytes"
	"io"
	"net"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
)

var (
	InfoHash = [20]byte{0x01, 0x02, 0x03} // truncated for brevity
	PeerID   = [20]byte{0x99, 0x88, 0x77} // truncated for brevity
)

func fakePeer(t *testing.T, ln net.Listener) {
	conn, _ := ln.Accept()

	defer conn.Close()

	buf := make([]byte, 68)
	_, _ = io.ReadFull(conn, buf)

	if buf[0] != 19 {
		t.Errorf("handshake pstrlen is %v, expected 19", buf[0])
	}

	response := new(bytes.Buffer)
	response.WriteByte(19)
	response.WriteString("BitTorrent protocol")
	response.Write(make([]byte, 8))
	response.Write(InfoHash[:])
	response.Write(PeerID[:])

	_, _ = conn.Write(response.Bytes())
}

func TestConnect(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen on TCP: %v", err)
	}
	defer ln.Close()

	go fakePeer(t, ln)

	addr := ln.Addr().(*net.TCPAddr)
	peer := Peer{
		Addr: net.JoinHostPort(addr.IP.String(), strconv.Itoa(addr.Port)),
	}

	pc := Connection{
		Peer:     &peer,
		InfoHash: InfoHash,
		PeerID:   PeerID,
	}

	err = pc.Connect()
	assert.Nil(t, err)
}
