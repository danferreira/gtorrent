package peers

import (
	"encoding/binary"
	"errors"
	"net"
	"strconv"
)

type Peer struct {
	SocketAddress string
}

func Unmarshal(buf []byte) (Peer, error) {
	if len(buf) != 6 {
		return Peer{}, errors.New("invalid peer address")
	}

	ip := net.IP(buf[:4])
	port := binary.BigEndian.Uint16(buf[4:])

	socketAddress := net.JoinHostPort(ip.String(), strconv.Itoa(int(port)))

	return Peer{
		SocketAddress: socketAddress,
	}, nil
}
