package peers

import (
	"encoding/binary"
	"errors"
	"net"
	"strconv"
)

type Peer struct {
	Addr string
}

func Unmarshal(buf []byte) (Peer, error) {
	if len(buf) != 6 {
		return Peer{}, errors.New("invalid peer address")
	}

	host := net.IP(buf[:4])
	port := binary.BigEndian.Uint16(buf[4:])

	return Peer{
		Addr: net.JoinHostPort(host.String(), strconv.Itoa(int(port))),
	}, nil
}
